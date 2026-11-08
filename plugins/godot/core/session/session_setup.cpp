#include "core/session/session.h"
#include "core/setup/ignore.h"
#include "core/version.h"

namespace nipa::session {

using json = nlohmann::json;

std::vector<setup::SidecarWarning> Session::sidecar_warnings() const {
    std::lock_guard<std::mutex> guard(mu_);
    return warnings_;
}

bool Session::load_meta_warnings() {
    json reply;
    try {
        run_on_link([&](const daemon::Client& client) {
            reply = client.call("MetaWarnings", {{"root", options_.root}, {"engine", "godot"}}, options_.unary_timeout,
                                &cancel_);
        });
    } catch (const daemon::DaemonError&) {
        return false;
    }
    auto list = setup::parse_meta_warnings(reply);
    bool changed = false;
    {
        std::lock_guard<std::mutex> guard(mu_);
        changed = list != warnings_;
        warnings_ = std::move(list);
    }
    if (changed) {
        Event e;
        e.kind = EventKind::WarningsChanged;
        events_.push(std::move(e));
    }
    return true;
}

std::int64_t Session::submit_meta_warnings() {
    {
        std::lock_guard<std::mutex> guard(mu_);
        if (warnings_pending_ || stopping_) {
            return 0;
        }
        warnings_pending_ = true;
    }
    return enqueue("meta-warnings", true, [this](std::int64_t) {
        {
            std::lock_guard<std::mutex> guard(mu_);
            warnings_pending_ = false;
        }
        load_meta_warnings();
        return Outcome{};
    });
}

setup::GodotDirState Session::probe_godot_dir(const std::string& project_prefix) {
    json reply;
    run_on_link([&](const daemon::Client& client) {
        reply = client.call("Status", {{"root", options_.root}}, options_.unary_timeout, &cancel_);
    });
    apply_status(reply);
    auto snapshot = status_cache().snapshot();
    auto state = setup::godot_dir_from_status(snapshot, project_prefix);
    json request = {{"branch", snapshot.branch}, {"path", setup::godot_dir(project_prefix)}, {"recursive", false}};
    try {
        run_on_link([&](const daemon::Client& client) {
            client.call("ProxyTreeManifest", {{"root", options_.root}, {"request", request}}, options_.unary_timeout,
                        &cancel_);
        });
        state.server = setup::Tracking::Tracked;
    } catch (const daemon::DaemonError& error) {
        state.server = setup::tracking_from_manifest(error.http_status, error.what());
        if (state.server == setup::Tracking::Unknown) {
            state.error = error.what();
        }
    }
    return state;
}

std::int64_t Session::submit_setup_check(setup::Inputs base) {
    return enqueue("setup", false, [this, base = std::move(base)](std::int64_t) {
        setup::Inputs in = base;
        in.plugin_version = kPluginVersion;
        in.min_version = options_.connect.min_version;
        in.ignore_content = setup::read_ignore(options_.root);
        in.binary = daemon::resolve_binary(options_.connect).value_or(std::string());
        try {
            run_on_link([](const daemon::Client&) {});
            in.connected = true;
            in.daemon_version = version();
        } catch (const daemon::CancelledError&) {
            throw;
        } catch (const std::exception& error) {
            in.connect_error = error.what();
        }
        if (in.connected) {
            try {
                in.godot_dir = probe_godot_dir(in.project_prefix);
            } catch (const daemon::DaemonError& error) {
                in.godot_dir.error = error.what();
            }
            in.sidecars_known = load_meta_warnings();
            in.sidecars = sidecar_warnings();
        }
        Outcome out{true, "setup checked"};
        out.data = {{"rows", setup::to_json(setup::evaluate(in))}, {"warnings", setup::to_json(in.sidecars)}};
        return out;
    });
}

}  // namespace nipa::session
