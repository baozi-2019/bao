// Package build 承载构建期注入的信息（版本号等）。
package build

// Version 是发布版本号：packaging/build-deb.sh 打包时经
// -ldflags "-X bao/internal/build.Version=<git tag>" 注入（tag 为唯一事实源）；
// 本地 go build 不注入，保持 dev 占位。
var Version = "dev"
