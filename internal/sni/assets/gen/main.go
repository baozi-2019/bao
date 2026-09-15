// 包子图标生成器：按 packaging/assets/bao.svg 的造型做 Go 原生矢量重绘，
// 不依赖任何外部工具。
//
// 思路：在 0..128 的设计空间里用解析距离场（SDF）描述每个图元
// （圆角矩形底、椭圆穹顶、胶囊线段褶皱），零等值面精确落在形状边界上；
// 每个目标尺寸以 8 倍画布做 8x8 超采样，再手写盒式平均降采样，
// 得到边缘平滑的 PNG。
//
// 运行（在仓库根目录）：go run ./internal/sni/assets/gen
// 输出：internal/sni/assets/icon_{16,22,24,32,48,64,128,256}.png
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// vec2 是设计空间（0..128）中的点。
type vec2 struct{ x, y float64 }

func v(x, y float64) vec2 { return vec2{x, y} }

// sdRoundRect 圆角矩形有符号距离：c 中心，b 半尺寸，r 圆角半径。
func sdRoundRect(p, c, b vec2, r float64) float64 {
	qx := math.Abs(p.x-c.x) - b.x + r
	qy := math.Abs(p.y-c.y) - b.y + r
	if qx < 0 && qy < 0 { // 内部直线区
		return math.Min(qx, qy)
	}
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) - r
}

// sdEllipse 椭圆有符号距离：c 中心，ab 半轴。
// 用 (|p/ab|-1)*min(ab) 近似：零等值面与精确椭圆完全一致，
// 只在抗锯齿梯度上略有偏差，而本实现靠超采样做抗锯齿，无需精确距离。
func sdEllipse(p, c, ab vec2) float64 {
	k := math.Hypot((p.x-c.x)/ab.x, (p.y-c.y)/ab.y)
	return (k - 1) * math.Min(ab.x, ab.y)
}

// distToSeg 点到线段距离的平方（避免开方，与半径平方比较即可）。
func dist2ToSeg(p, a, b vec2) float64 {
	bax, bay := b.x-a.x, b.y-a.y
	d := bax*bax + bay*bay
	t := 0.0
	if d > 0 {
		t = ((p.x-a.x)*bax + (p.y-a.y)*bay) / d
		t = math.Max(0, math.Min(1, t))
	}
	dx, dy := p.x-(a.x+t*bax), p.y-(a.y+t*bay)
	return dx*dx + dy*dy
}

// cubic 把三次贝塞尔段采样成折线（含起点，steps+1 个点）。
func cubic(p0, c1, c2, p3 vec2, steps int) []vec2 {
	pts := make([]vec2, 0, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mt := 1 - t
		x := mt*mt*mt*p0.x + 3*mt*mt*t*c1.x + 3*mt*t*t*c2.x + t*t*t*p3.x
		y := mt*mt*mt*p0.y + 3*mt*mt*t*c1.y + 3*mt*t*t*c2.y + t*t*t*p3.y
		pts = append(pts, v(x, y))
	}
	return pts
}

// polyline 折线首尾相接合并。
func polyline(segs ...[]vec2) []vec2 {
	out := make([]vec2, 0, len(segs)*24)
	for _, s := range segs {
		if len(out) > 0 && len(s) > 0 && out[len(out)-1] == s[0] {
			out = append(out, s[1:]...)
		} else {
			out = append(out, s...)
		}
	}
	return out
}

var (
	// 顶部螺旋褶皱（#DEC49A，线宽 3，源自 bao.svg 的三条 c 曲线）
	spiralStrokes = [][]vec2{
		polyline(
			cubic(v(64, 46), v(59, 49), v(58, 54), v(61, 57), 24),
			cubic(v(61, 57), v(64, 60), v(70, 59), v(71, 55), 24),
			cubic(v(71, 55), v(72, 50), v(68, 47), v(63, 47), 24),
			cubic(v(63, 47), v(57, 47), v(53, 51), v(53, 57), 24),
		),
		cubic(v(64, 42), v(55, 44), v(50, 51), v(52, 58), 24),
		cubic(v(64, 42), v(73, 44), v(78, 51), v(76, 58), 24),
	}
	// 放射状褶纹（#EAD6B4，线宽 3）
	radialStrokes = [][]vec2{
		cubic(v(52, 58), v(46, 66), v(40, 72), v(34, 76), 24),
		cubic(v(58, 64), v(55, 72), v(51, 78), v(47, 82), 24),
		cubic(v(76, 58), v(82, 66), v(88, 72), v(94, 76), 24),
		cubic(v(70, 64), v(73, 72), v(77, 78), v(81, 82), 24),
	}
)

// strokeHit 报告点 p 是否落在任一褶皱折线的圆头笔画内，r 为笔画半径。
func strokeHit(p vec2, strokes [][]vec2, r float64) bool {
	r2 := r * r
	for _, s := range strokes {
		for i := 0; i+1 < len(s); i++ {
			if dist2ToSeg(p, s[i], s[i+1]) <= r2 {
				return true
			}
		}
	}
	return false
}

// lerpColor 线性插值（sRGB 空间近似，图标场景足够）。
func lerpColor(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
}

// blendOver 把 src（不透明度乘 alpha）以 source-over 方式叠到 dst 上。
func blendOver(dst color.NRGBA, src color.NRGBA, alpha float64) color.NRGBA {
	sa := float64(src.A) / 255 * alpha
	da := float64(dst.A) / 255
	outA := sa + da*(1-sa)
	if outA <= 0 {
		return color.NRGBA{}
	}
	mix := func(s, d uint8) uint8 {
		return uint8(math.Round((float64(s)*sa + float64(d)*da*(1-sa)) / outA))
	}
	return color.NRGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), uint8(math.Round(outA * 255))}
}

// 调色板（取自 bao.svg）。
var (
	colBgTop     = color.NRGBA{0xFF, 0xAB, 0x54, 255}
	colBgBottom  = color.NRGBA{0xEE, 0x7F, 0x2E, 255}
	colBunTop    = color.NRGBA{0xFF, 0xFD, 0xF7, 255}
	colBunBottom = color.NRGBA{0xF3, 0xE3, 0xC6, 255}
	colBunDark   = color.NRGBA{0xE7, 0xD0, 0xAA, 255}
	colSpiral    = color.NRGBA{0xDE, 0xC4, 0x9A, 255}
	colRadial    = color.NRGBA{0xEA, 0xD6, 0xB4, 255}
	colHighlight = color.NRGBA{0xFF, 0xFF, 0xFF, 255}
)

// paint 对设计空间中的单个子像素采样点着色。
func paint(p vec2) color.NRGBA {
	// 蒸笼底：圆角矩形 + 纵向渐变
	if sdRoundRect(p, v(64, 64), v(60, 60), 26) > 0 {
		return color.NRGBA{}
	}
	c := lerpColor(colBgTop, colBgBottom, (p.y-4)/120)

	// 包子主体：穹顶椭圆与下缘曲线的交集——
	// 下缘 y = 88 + 19·(1-(|dx|/38)²)^0.75：两侧收于 (26,88)/(102,88)，
	// 中间鼓到 107，整体近似 bao.svg 半球+鼓底 path 的剪影。
	dx := math.Abs(p.x - 64)
	yBottom := 88 + 19*math.Pow(math.Max(0, 1-(dx/38)*(dx/38)), 0.75)
	if sdEllipse(p, v(64, 88), v(38, 50)) <= 0 && p.y <= yBottom {
		c = lerpColor(colBunTop, colBunBottom, (p.y-38)/68)
		// 底部暗面：内弧线（26,90)→(64,102)→(102,90) 以下的细新月带，
		// 与 SVG 暗面 path 同形——中心约 4px 厚，向两侧收敛于下缘。
		inner := 90 + 12*(1-(dx/38)*(dx/38))
		if p.y > inner {
			c = blendOver(c, colBunDark, 0.8)
		}
		// 放射褶纹先画（SVG 中后画者居上，螺旋在上）
		if strokeHit(p, radialStrokes, 1.5) {
			c = colRadial
		}
		if strokeHit(p, spiralStrokes, 1.5) {
			c = colSpiral
		}
		// 高光
		if sdEllipse(p, v(46, 56), v(11, 6)) <= 0 {
			c = blendOver(c, colHighlight, 0.55)
		}
	}
	return c
}

// render 以 8 倍画布 8x8 超采样渲染目标尺寸 N，盒式平均降采样。
func render(n int) *image.NRGBA {
	const s = 8 // 超采样倍率
	scale := 128.0 / float64(n)
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	inv := 1.0 / float64(s*s)
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			var ar, ag, ab, aa float64
			for sy := 0; sy < s; sy++ {
				for sx := 0; sx < s; sx++ {
					fx := (float64(px) + (float64(sx)+0.5)/s) * scale
					fy := (float64(py) + (float64(sy)+0.5)/s) * scale
					c := paint(v(fx, fy))
					ar += float64(c.R)
					ag += float64(c.G)
					ab += float64(c.B)
					aa += float64(c.A)
				}
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(ar * inv)),
				G: uint8(math.Round(ag * inv)),
				B: uint8(math.Round(ab * inv)),
				A: uint8(math.Round(aa * inv)),
			})
		}
	}
	return img
}

func main() {
	sizes := []int{16, 22, 24, 32, 48, 64, 128, 256}
	outDir := "internal/sni/assets"
	for _, n := range sizes {
		img := render(n)
		path := filepath.Join(outDir, fmt.Sprintf("icon_%d.png", n))
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "创建文件:", err)
			os.Exit(1)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			fmt.Fprintln(os.Stderr, "编码 PNG:", err)
			os.Exit(1)
		}
		f.Close()
		fmt.Println("已生成", path)
	}
}
