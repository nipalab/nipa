cmake_minimum_required(VERSION 3.24)

project(nipa_godot LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

option(NIPA_BUILD_TESTS "Build the core unit tests" ON)
option(NIPA_BUILD_EXTENSION "Build the godot-cpp GDExtension" OFF)
option(NIPA_COVERAGE "Instrument core and tests for gcov" OFF)

find_package(Threads REQUIRED)

set(NIPA_THIRDPARTY ${CMAKE_CURRENT_SOURCE_DIR}/thirdparty)

add_library(nipa_core STATIC
  core/version.cpp
  core/daemon/cancel.cpp
  core/daemon/client.cpp
  core/daemon/connector.cpp
  core/daemon/endpoint.cpp
  core/daemon/messages.cpp
  core/daemon/spawn.cpp
  core/paths/project_paths.cpp
  core/locks/lock_policy.cpp
  core/locks/readonly_guard.cpp
  core/status/status.cpp
  core/diff/diff.cpp
  core/session/event_queue.cpp
  core/session/session.cpp
  core/session/session_ops.cpp
  core/conflicts/conflicts.cpp
  core/conflicts/merge_tool.cpp
  core/history/history.cpp
  core/preview/preview.cpp
)
set_target_properties(nipa_core PROPERTIES POSITION_INDEPENDENT_CODE ON)
target_include_directories(nipa_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR})
target_include_directories(nipa_core SYSTEM PUBLIC
  ${NIPA_THIRDPARTY}/cpp-httplib
  ${NIPA_THIRDPARTY}/nlohmann-json)
target_link_libraries(nipa_core PUBLIC Threads::Threads)
if(WIN32)
  target_link_libraries(nipa_core PUBLIC ws2_32)
endif()

if(MSVC)
  target_compile_options(nipa_core PRIVATE /W4 /WX)
else()
  target_compile_options(nipa_core PRIVATE -Wall -Wextra -Wpedantic -Werror)
endif()

if(NIPA_COVERAGE AND NOT MSVC)
  target_compile_options(nipa_core PRIVATE --coverage -O0 -g)
  target_link_options(nipa_core PUBLIC --coverage)
endif()

if(NIPA_BUILD_TESTS)
  enable_testing()
  add_executable(nipa_core_tests
    core/tests/test_main.cpp
    core/tests/fake_daemon.cpp
    core/tests/test_version.cpp
    core/tests/test_endpoint.cpp
    core/tests/test_client.cpp
    core/tests/test_connector.cpp
    core/tests/test_paths.cpp
    core/tests/test_status.cpp
    core/tests/test_diff.cpp
    core/tests/test_messages.cpp
    core/tests/test_session.cpp
    core/tests/test_locks.cpp
    core/tests/test_readonly_guard.cpp
    core/tests/test_conflicts.cpp
    core/tests/test_merge_tool.cpp
    core/tests/test_history.cpp
  )
  target_include_directories(nipa_core_tests SYSTEM PRIVATE ${NIPA_THIRDPARTY}/doctest)
  target_link_libraries(nipa_core_tests PRIVATE nipa_core)
  if(NIPA_COVERAGE AND NOT MSVC)
    target_compile_options(nipa_core_tests PRIVATE --coverage -O0 -g)
    target_link_options(nipa_core_tests PRIVATE --coverage)
  endif()
  add_test(NAME nipa_core_tests COMMAND nipa_core_tests)
endif()

if(NIPA_BUILD_EXTENSION)
  add_subdirectory(extension)
endif()
