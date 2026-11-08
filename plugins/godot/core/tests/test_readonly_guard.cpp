#include <doctest.h>

#include <filesystem>
#include <fstream>

#include "core/locks/readonly_guard.h"
#include "fake_daemon.h"

namespace fs = std::filesystem;
using nipa::locks::ReadOnlyGuard;

namespace {

struct Clone {
    std::string root = nipa::test::make_temp_dir();

    ~Clone() {
        std::error_code error;
        for (auto it = fs::recursive_directory_iterator(root, error); !error && it != fs::recursive_directory_iterator();
             it.increment(error)) {
            fs::permissions(it->path(), fs::perms::owner_all, fs::perm_options::add, error);
        }
        fs::remove_all(root, error);
    }

    void write(const std::string& path, const std::string& content = "x") const {
        fs::create_directories((fs::path(root) / path).parent_path());
        std::ofstream(fs::path(root) / path) << content;
    }

    bool writable(const std::string& path) const {
        return (fs::status(fs::path(root) / path).permissions() & fs::perms::owner_write) != fs::perms::none;
    }

    std::string state() const { return root + "/.godot/nipa/readonly.json"; }
};

}  // namespace

TEST_CASE("guard makes a locked unit read-only and restores it on release") {
    Clone clone;
    clone.write("art/a.png");
    clone.write("art/a.png.import");
    clone.write("art/b.png");
    fs::permissions(fs::path(clone.root) / "art/a.png.import", fs::perms::owner_read | fs::perms::owner_write | fs::perms::group_write,
                    fs::perm_options::replace);

    ReadOnlyGuard guard(clone.root, clone.state());
    auto result = guard.apply({"art/a.png"});
    CHECK(result.changed());
    CHECK(result.guarded == std::vector<std::string>{"art/a.png", "art/a.png.import"});
    CHECK_FALSE(clone.writable("art/a.png"));
    CHECK_FALSE(clone.writable("art/a.png.import"));
    CHECK(clone.writable("art/b.png"));
    CHECK(fs::exists(clone.state()));

    CHECK_FALSE(guard.apply({"art/a.png"}).changed());

    auto released = guard.release_all();
    CHECK(released.released.size() == 2);
    CHECK(clone.writable("art/a.png"));
    CHECK((fs::status(fs::path(clone.root) / "art/a.png.import").permissions() & fs::perms::group_write) != fs::perms::none);
    CHECK(guard.guarded().empty());
    CHECK_FALSE(fs::exists(clone.state()));
}

TEST_CASE("guard covers every file under a directory lock but never .nipa") {
    Clone clone;
    clone.write("levels/one.tscn");
    clone.write("levels/sub/two.tscn");
    clone.write(".nipa/config");

    ReadOnlyGuard guard(clone.root, "");
    auto result = guard.apply({"levels", ".nipa", "", "missing.png"});
    CHECK(guard.guarded() == std::set<std::string>{"levels/one.tscn", "levels/sub/two.tscn"});
    CHECK(result.guarded.size() == 2);
    CHECK(clone.writable(".nipa/config"));

    guard.apply({"levels/one.tscn"});
    CHECK(clone.writable("levels/sub/two.tscn"));
    CHECK_FALSE(clone.writable("levels/one.tscn"));
}

TEST_CASE("guard leaves files that were already read-only alone") {
    Clone clone;
    clone.write("a.png");
    fs::permissions(fs::path(clone.root) / "a.png", fs::perms::owner_read, fs::perm_options::replace);

    ReadOnlyGuard guard(clone.root, "");
    CHECK_FALSE(guard.apply({"a.png"}).changed());
    guard.release_all();
    CHECK_FALSE(clone.writable("a.png"));
}

TEST_CASE("guard re-protects a replaced file and forgets deleted ones") {
    Clone clone;
    clone.write("a.png");
    clone.write("b.png");
    ReadOnlyGuard guard(clone.root, "");
    guard.apply({"a.png", "b.png"});

    fs::remove(fs::path(clone.root) / "a.png");
    clone.write("a.png", "new");
    fs::remove(fs::path(clone.root) / "b.png");
    auto result = guard.apply({"a.png"});
    CHECK(result.guarded == std::vector<std::string>{"a.png"});
    CHECK(result.released.empty());
    CHECK_FALSE(clone.writable("a.png"));
    CHECK(guard.guarded() == std::set<std::string>{"a.png"});
}

TEST_CASE("a later session restores what a crashed one guarded") {
    Clone clone;
    clone.write("a.png");
    {
        ReadOnlyGuard first(clone.root, clone.state());
        first.apply({"a.png"});
    }
    CHECK_FALSE(clone.writable("a.png"));

    ReadOnlyGuard second(clone.root, clone.state());
    second.load();
    CHECK(second.guarded() == std::set<std::string>{"a.png"});
    second.release_all();
    CHECK(clone.writable("a.png"));
}

TEST_CASE("guard ignores a missing or malformed state file") {
    Clone clone;
    ReadOnlyGuard missing(clone.root, clone.state());
    missing.load();
    CHECK(missing.guarded().empty());

    fs::create_directories(fs::path(clone.state()).parent_path());
    std::ofstream(clone.state()) << "[1,2]";
    ReadOnlyGuard malformed(clone.root, clone.state());
    malformed.load();
    CHECK(malformed.guarded().empty());

    std::ofstream(clone.state()) << R"({"a.png": "x", "b.png": 420})";
    ReadOnlyGuard partial(clone.root, clone.state());
    partial.load();
    CHECK(partial.guarded() == std::set<std::string>{"b.png"});

    ReadOnlyGuard memory(clone.root, "");
    memory.load();
    CHECK(memory.guarded().empty());
}
