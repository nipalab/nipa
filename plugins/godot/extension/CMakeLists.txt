include(FetchContent)

set(GODOTCPP_ENABLE_TESTING OFF CACHE BOOL "" FORCE)
FetchContent_Declare(
  godot-cpp
  GIT_REPOSITORY https://github.com/godotengine/godot-cpp.git
  GIT_TAG godot-4.4-stable
  GIT_SHALLOW TRUE
)
FetchContent_MakeAvailable(godot-cpp)

# Platform folder and file names must match addons/nipa/nipa.gdextension.
# macOS builds one universal library when CMAKE_OSX_ARCHITECTURES="arm64;x86_64".
if(WIN32)
  set(NIPA_PLATFORM "windows.x86_64")
  set(NIPA_LIB_PREFIX "")
elseif(APPLE)
  set(NIPA_PLATFORM "macos")
  set(NIPA_LIB_PREFIX "lib")
elseif(CMAKE_SYSTEM_PROCESSOR MATCHES "^(aarch64|arm64|ARM64|armv8.*)$")
  set(NIPA_PLATFORM "linux.arm64")
  set(NIPA_LIB_PREFIX "lib")
else()
  set(NIPA_PLATFORM "linux.x86_64")
  set(NIPA_LIB_PREFIX "lib")
endif()
set(NIPA_BIN_DIR "${CMAKE_CURRENT_SOURCE_DIR}/../addons/nipa/bin/${NIPA_PLATFORM}")

add_library(nipa SHARED
  register_types.cpp
  nipa_request.cpp
  nipa_session.cpp
  nipa_vcs_interface.cpp
)
target_link_libraries(nipa PRIVATE nipa_core godot-cpp::editor)
set_target_properties(nipa PROPERTIES
  PREFIX "${NIPA_LIB_PREFIX}"
  OUTPUT_NAME "nipa.${NIPA_PLATFORM}.editor"
  LIBRARY_OUTPUT_DIRECTORY "$<1:${NIPA_BIN_DIR}>"
  RUNTIME_OUTPUT_DIRECTORY "$<1:${NIPA_BIN_DIR}>"
)
target_include_directories(nipa PRIVATE ${CMAKE_CURRENT_SOURCE_DIR})
