#include <doctest.h>

#include "core/version.h"

TEST_CASE("version_older compares dotted numbers") {
    using nipa::version_older;
    CHECK(version_older("0.1.0", "0.2.0"));
    CHECK(version_older("0.2.0", "0.2.1"));
    CHECK(version_older("v0.9", "1.0.0"));
    CHECK_FALSE(version_older("0.2.0", "0.2.0"));
    CHECK_FALSE(version_older("1.0.0", "0.9.9"));
    CHECK_FALSE(version_older("0.2", "0.2.0"));
    CHECK(version_older("0.2", "0.2.1"));
}

TEST_CASE("version_older ignores unknown minimums and development builds") {
    using nipa::version_older;
    CHECK_FALSE(version_older("0.1.0", ""));
    CHECK_FALSE(version_older("0.1.0", "dev"));
    CHECK_FALSE(version_older("dev-abc123", "1.0.0"));
    CHECK_FALSE(version_older("", "1.0.0"));
}

TEST_CASE("plugin version is set") {
    CHECK(std::string(nipa::kPluginVersion) == "0.1.0");
}
