#include "core/daemon/spawn.h"

#if defined(_WIN32)
#include <windows.h>
#else
#include <unistd.h>
#include <sys/wait.h>
#endif

#include <vector>

namespace nipa::daemon {

#if defined(_WIN32)

bool spawn_detached(const std::string& binary, const std::string& endpoint_path) {
    std::string command = "\"" + binary + "\" serve";
    if (!endpoint_path.empty()) {
        command += " --endpoint \"" + endpoint_path + "\"";
    }
    std::vector<char> mutable_command(command.begin(), command.end());
    mutable_command.push_back('\0');
    STARTUPINFOA info{};
    info.cb = sizeof(info);
    PROCESS_INFORMATION process{};
    BOOL ok = CreateProcessA(nullptr, mutable_command.data(), nullptr, nullptr, FALSE,
                             DETACHED_PROCESS | CREATE_NO_WINDOW, nullptr, nullptr, &info, &process);
    if (!ok) {
        return false;
    }
    CloseHandle(process.hThread);
    CloseHandle(process.hProcess);
    return true;
}

#else

bool spawn_detached(const std::string& binary, const std::string& endpoint_path) {
    pid_t pid = fork();
    if (pid < 0) {
        return false;
    }
    if (pid == 0) {
        setsid();
        if (endpoint_path.empty()) {
            execl(binary.c_str(), binary.c_str(), "serve", static_cast<char*>(nullptr));
        } else {
            execl(binary.c_str(), binary.c_str(), "serve", "--endpoint", endpoint_path.c_str(),
                  static_cast<char*>(nullptr));
        }
        _exit(127);
    }
    return true;
}

#endif

}  // namespace nipa::daemon
