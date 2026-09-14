package calc

import (
	"math"
	"testing"
)

func TestEval(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"1+2", 3},
		{"2+3*4", 14},
		{"2*3+4", 10},
		{"(2+3)*4", 20},
		{"10/4", 2.5},
		{"10%3", 1},
		{"2^10", 1024},
		{"2^3^2", 512}, // 右结合：2^(3^2)
		{"-2^2", -4},   // 一元负号优先级低于幂：-(2^2)
		{"2^-3", 0.125},
		{"--5", 5},
		{"+-5", -5},
		{"+5", 5},
		{"-(3-5)", 2},
		{" 1 + 2 ", 3}, // 空白应被忽略
		{"1e3", 1000},
		{"1.5E-2", 0.015},
		{".5*2", 1},
		{"3.", 3},
		{"1.5+2.25", 3.75},
		{"100-(50+25)/5", 85},
		{"sqrt(9)+1", 4},
		{"sqrt(sqrt(81))", 3},
		{"2*(3+4)^2", 98},
		{"abs(-3)", 3},
		{"abs(2-5)", 3},
		{"sin(0)", 0},
		{"cos(0)", 1},
		{"tan(0)", 0},
		{"ln(1)", 0},
		{"log(1000)", 3},
		{"sqrt(2)", math.Sqrt(2)},
		{"ln(10)", math.Log(10)},
		{"log(2)+log(5)", 1},
	}
	for _, c := range cases {
		got, ok := Eval(c.expr)
		if !ok {
			t.Errorf("Eval(%q) 返回失败，期望 %v", c.expr, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("Eval(%q) = %v，期望 %v", c.expr, got, c.want)
		}
	}
}

func TestEvalInvalid(t *testing.T) {
	invalid := []string{
		"", "   ", "1+", "+", "-", "*3", "(", ")",
		"(1+2", "1+2)", "()", "sin()",
		"abc", "1a", "1 2", "1.2.3", "1e",
		"1,2", "2^^3", "2^", "1++", "1+-",
		"sqrt 9", "min(1,2)", "unknown(1)",
		"sqrt(-1)", // 结果 NaN
		"1/0",      // 结果 +Inf
		"5%0",      // 结果 NaN
		"0*Inf(1)", // Inf 不是合法 token，词法即失败
	}
	for _, s := range invalid {
		if v, ok := Eval(s); ok {
			t.Errorf("Eval(%q) = %v, true，期望失败", s, v)
		}
	}
}

func TestLooksLikeMath(t *testing.T) {
	yes := []string{"1+1", "0.1", "(1)", "  (1+2)", "-5", "+5", ".5", "  \t.25*4"}
	for _, s := range yes {
		if !LooksLikeMath(s) {
			t.Errorf("LooksLikeMath(%q) = false，期望 true", s)
		}
	}
	no := []string{"", "   ", "abc", "*1", "/2", "^3", "sqrt(9)", "版本", "e5", " x+1"}
	for _, s := range no {
		if LooksLikeMath(s) {
			t.Errorf("LooksLikeMath(%q) = true，期望 false", s)
		}
	}
}
