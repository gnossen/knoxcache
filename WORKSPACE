workspace(name = "org_gnossen_knoxcache")

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

http_archive(
    name = "io_bazel_rules_go",
    sha256 = "7904dbecbaffd068651916dce77ff3437679f9d20e1a7956bff43826e7645fcc",
    urls = [
        "https://github.com/bazelbuild/rules_go/archive/refs/tags/v0.34.0.tar.gz",
    ],
)

http_archive(
    name = "bazel_gazelle",
    sha256 = "222e49f034ca7a1d1231422cdb67066b885819885c356673cb1f72f748a3c9d4",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/bazel-gazelle/releases/download/v0.26.0/bazel-gazelle-v0.26.0.tar.gz",
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.26.0/bazel-gazelle-v0.26.0.tar.gz",
    ],
)

load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")
load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies", "go_repository")

go_repository(
    name = "org_golang_x_net",
    importpath = "golang.org/x/net",
    sum = "h1:wjuX4b5yYQnEQHzd+CBcrcC6OVR2J1CN6mUy0oSxIPo=",
    version = "v0.0.0-20210525063256-abc453219eb5",
)

go_repository(
    name = "com_github_jinzhu_now",
    importpath = "github.com/jinzhu/now",
    sum = "h1:tHnRBy1i5F2Dh8BAFxqFzxKqqvezXrL2OW1TnX+Mlas=",
    version = "v1.1.4",
)

go_repository(
    name = "com_github_jinzhu_inflection",
    importpath = "github.com/jinzhu/inflection",
    sum = "h1:K317FqzuhWc8YvSVlFMCCUb36O/S9MCKRDI7QkRKD/E=",
    version = "v1.0.0",
)

go_repository(
    name = "com_github_go_gorm_gorm",
    importpath = "gorm.io/gorm",
    sum = "h1:h8sGJ+biDgBA1AD1Ha9gFCx7h8npU7AsLdlkX0n2TpE=",
    version = "v1.23.8",
)

go_repository(
    name = "com_github_mattn_go_sqlite3",
    importpath = "github.com/mattn/go-sqlite3",
    sum = "h1:TJ1bhYJPV44phC+IMu1u2K/i5RriLTPe+yc68XDJ1Z0=",
    version = "v1.14.12",
)

go_repository(
    name = "io_gorm_driver_sqlite",
    importpath = "gorm.io/driver/sqlite",
    sum = "h1:Fi8xNYCUplOqWiPa3/GuCeowRNBRGTf62DEmhMDHeQQ=",
    version = "v1.3.6",
)

go_repository(
    name = "org_golang_x_sys",
    importpath = "golang.org/x/sys",
    sum = "h1:b3NXsE2LusjYGGjL5bxEVZZORm/YEFFrWFjR8eFrw/c=",
    version = "v0.0.0-20210423082822-04245dca01da",
)

go_repository(
    name = "org_golang_x_term",
    importpath = "golang.org/x/term",
    sum = "h1:v+OssWQX+hTHEmOBgwxdZxK4zHq3yOs8F9J7mk0PY8E=",
    version = "v0.0.0-20201126162022-7de9c90e9dd1",
)

go_repository(
    name = "org_golang_x_text",
    importpath = "golang.org/x/text",
    sum = "h1:aRYxNxv6iGQlyVaZmk6ZgYEDa+Jg18DxebPSrd6bg1M=",
    version = "v0.3.6",
)


go_rules_dependencies()

go_register_toolchains(version = "1.17.13")

gazelle_dependencies()
