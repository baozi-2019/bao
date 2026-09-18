#!/bin/sh
# 构建 bao 的 .deb 包：编译二进制 → 装配文件树 → dpkg-deb 打包。
# 产物：dist/bao_<VERSION>_amd64.deb
# 版本号以 git tag 为唯一事实源：当前提交必须有精确匹配的 tag 才能构建，
# 避免脚本内硬编码 VERSION 与 tag 漂移（0.3.0 曾因此漏改）。
set -eu

REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$REPO"
if ! VERSION=$(git describe --tags --exact-match 2>/dev/null); then
  echo "错误：HEAD 没有精确匹配的 git tag，请先打 tag 再构建发布包" >&2
  exit 1
fi
VERSION=${VERSION#v}

DIST="$REPO/dist"
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

# -s -w 裁剪符号表与 DWARF 调试信息，减小发布体积；
# -X 注入版本号（关于对话框展示），与 tag 保持同一事实源。
go build -ldflags "-s -w -X bao/internal/build.Version=$VERSION" -o bao .

mkdir -p "$STAGE/DEBIAN" \
  "$STAGE/usr/bin" \
  "$STAGE/usr/share/applications" \
  "$STAGE/usr/share/icons/hicolor/scalable/apps" \
  "$STAGE/etc/xdg/autostart"

install -m 0755 bao                            "$STAGE/usr/bin/bao"
install -m 0755 packaging/assets/bao-keybind   "$STAGE/usr/bin/bao-keybind"
install -m 0755 packaging/assets/bao-autostart "$STAGE/usr/bin/bao-autostart"
install -m 0644 packaging/assets/bao.desktop   "$STAGE/usr/share/applications/bao.desktop"
install -m 0644 packaging/assets/bao-autostart.desktop "$STAGE/etc/xdg/autostart/bao.desktop"
install -m 0644 packaging/assets/bao.svg       "$STAGE/usr/share/icons/hicolor/scalable/apps/bao.svg"

# Installed-Size 只统计安装到系统的 usr/etc，不含 DEBIAN 元数据目录。
SIZE=$(du -sk "$STAGE/usr" "$STAGE/etc" | awk '{s+=$1} END {print s}')
cat > "$STAGE/DEBIAN/control" <<EOF
Package: bao
Version: $VERSION
Section: utils
Priority: optional
Architecture: amd64
Installed-Size: $SIZE
Maintainer: baozi <baozi@localhost>
Depends: libgtk-4-1, libglib2.0-bin
Description: Albert 风格的 GTK4 桌面启动器
 应用/文件模糊搜索 + 计算器，支持 apps/files 触发器过滤。
 安装后：应用菜单出现 bao；/etc/xdg/autostart 隐藏自启；
 首次自启自动绑定 GNOME 全局快捷键 Super+空格（可改）。
EOF

cat > "$STAGE/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -f -t /usr/share/icons/hicolor >/dev/null 2>&1 || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
exit 0
EOF
chmod 0755 "$STAGE/DEBIAN/postinst"

mkdir -p "$DIST"
dpkg-deb --root-owner-group --build "$STAGE" "$DIST/bao_${VERSION}_amd64.deb"
echo "已生成 $DIST/bao_${VERSION}_amd64.deb"
