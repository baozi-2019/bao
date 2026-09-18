// Package calc 提供严格模式的数学表达式求值。
//
// 语法为手写递归下降解析：
//
//	expr    := term (('+' | '-') term)*
//	term    := unary (('*' | '/' | '%') unary)*
//	unary   := ('-' | '+') unary | factor
//	factor  := primary ('^' unary)?   // 幂运算右结合，且优先级高于一元负号：-2^2 == -4；指数允许一元负号：2^-3
//	primary := number | func '(' expr ')' | '(' expr ')'
//
// 支持的运算符：+ - * / % ^；支持的函数：sqrt、abs、sin、cos、tan、ln（自然对数）、log（常用对数）。
// 数字支持小数（.5、3.）与科学计数法（1e3、1.5E-2）。
//
// 严格模式：整串（忽略空白）必须是合法表达式；多余字符、未知函数、
// 除零或结果为非有限值（NaN/Inf）均视为失败（ok == false）。
package calc

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// errInvalid 表示表达式非法：词法错误、语法错误或未知函数。
var errInvalid = errors.New("非法表达式")

// maxDepth 是解析递归深度上限：嵌套括号/一元号层数超过即视为非法，
// 防止粘贴病态长串（海量嵌套）导致递归栈耗尽。
const maxDepth = 512

// Eval 严格模式求值：整串 expr（忽略空白）必须是合法表达式，
// 返回其数值。任何错误或非有限结果都返回 ok == false。
func Eval(expr string) (value float64, ok bool) {
	tokens, err := lex(expr)
	if err != nil {
		return 0, false
	}
	p := &parser{tokens: tokens}
	v, err := p.parseExpr()
	if err != nil {
		return 0, false
	}
	if p.peek().kind != tokEOF {
		return 0, false // 有多余 token，未消费完整串
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// LooksLikeMath 报告 s 是否像数学表达式：跳过前导空白后，
// 首字符为数字、'('、'-'、'+' 或 '.' 时返回 true。
func LooksLikeMath(s string) bool {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s == "" {
		return false
	}
	c := s[0]
	return isDigit(c) || c == '(' || c == '-' || c == '+' || c == '.'
}

// tokenKind 是词法单元类别。
type tokenKind int

const (
	tokNum   tokenKind = iota // 数字字面量
	tokIdent                  // 标识符（函数名）
	tokOp                     // 运算符：+ - * / % ^
	tokLParen
	tokRParen
	tokEOF
)

// token 是单个词法单元；num 为数字字面量的值，text 为标识符或运算符原文。
type token struct {
	kind tokenKind
	num  float64
	text string
}

// lex 将整串切分为词法单元；遇到非法字符返回错误。
func lex(s string) ([]token, error) {
	var tokens []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isDigit(c) || c == '.':
			start := i
			for i < len(s) && (isDigit(s[i]) || s[i] == '.') {
				i++
			}
			// 可选指数部分：e/E 后跟可选符号与至少一位数字
			if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
				j := i + 1
				if j < len(s) && (s[j] == '+' || s[j] == '-') {
					j++
				}
				if j < len(s) && isDigit(s[j]) {
					i = j
					for i < len(s) && isDigit(s[i]) {
						i++
					}
				}
			}
			v, err := strconv.ParseFloat(s[start:i], 64)
			if err != nil {
				return nil, errInvalid
			}
			tokens = append(tokens, token{kind: tokNum, num: v})
		case isLetter(c):
			start := i
			for i < len(s) && isLetter(s[i]) {
				i++
			}
			tokens = append(tokens, token{kind: tokIdent, text: s[start:i]})
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '%' || c == '^':
			tokens = append(tokens, token{kind: tokOp, text: string(c)})
			i++
		case c == '(':
			tokens = append(tokens, token{kind: tokLParen})
			i++
		case c == ')':
			tokens = append(tokens, token{kind: tokRParen})
			i++
		default:
			return nil, errInvalid
		}
	}
	return append(tokens, token{kind: tokEOF}), nil
}

// parser 是对 token 流做递归下降解析的游标；depth 记录当前嵌套深度。
type parser struct {
	tokens []token
	pos    int
	depth  int
}

// enter 进入一层递归；超过 maxDepth 返回 errInvalid。
func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return errInvalid
	}
	return nil
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) next() token {
	t := p.tokens[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

// atOp 报告当前 token 是否为给定运算符。
func (p *parser) atOp(op string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == op
}

// parseExpr 解析加减层：term (('+' | '-') term)*。
func (p *parser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for p.atOp("+") || p.atOp("-") {
		op := p.next().text
		rhs, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == "+" {
			v += rhs
		} else {
			v -= rhs
		}
	}
	return v, nil
}

// parseTerm 解析乘除模层：unary (('*' | '/' | '%') unary)*。
func (p *parser) parseTerm() (float64, error) {
	v, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for p.atOp("*") || p.atOp("/") || p.atOp("%") {
		op := p.next().text
		rhs, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		switch op {
		case "*":
			v *= rhs
		case "/":
			v /= rhs
		default:
			v = math.Mod(v, rhs)
		}
	}
	return v, nil
}

// parseUnary 解析一元正负号，可嵌套：--5 == 5。
func (p *parser) parseUnary() (float64, error) {
	if err := p.enter(); err != nil {
		return 0, err
	}
	defer func() { p.depth-- }()
	if p.atOp("-") || p.atOp("+") {
		op := p.next().text
		v, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		if op == "-" {
			return -v, nil
		}
		return v, nil
	}
	return p.parseFactor()
}

// parseFactor 解析幂运算，右结合：2^3^2 == 2^(3^2)。
// 一元负号优先级低于幂：-2^2 == -(2^2) == -4；指数允许一元负号：2^-3 == 0.125。
func (p *parser) parseFactor() (float64, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return 0, err
	}
	if p.atOp("^") {
		p.next()
		exp, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

// parsePrimary 解析数字、函数调用或括号表达式。
func (p *parser) parsePrimary() (float64, error) {
	if err := p.enter(); err != nil {
		return 0, err
	}
	defer func() { p.depth-- }()
	t := p.peek()
	switch t.kind {
	case tokNum:
		p.next()
		return t.num, nil
	case tokIdent:
		p.next()
		if p.peek().kind != tokLParen {
			return 0, errInvalid
		}
		p.next()
		arg, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.peek().kind != tokRParen {
			return 0, errInvalid
		}
		p.next()
		return applyFunc(t.text, arg)
	case tokLParen:
		p.next()
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if p.peek().kind != tokRParen {
			return 0, errInvalid
		}
		p.next()
		return v, nil
	default:
		return 0, errInvalid
	}
}

// applyFunc 对单参数函数名求值；未知函数返回错误。
func applyFunc(name string, x float64) (float64, error) {
	switch name {
	case "sqrt":
		return math.Sqrt(x), nil
	case "abs":
		return math.Abs(x), nil
	case "sin":
		return math.Sin(x), nil
	case "cos":
		return math.Cos(x), nil
	case "tan":
		return math.Tan(x), nil
	case "ln":
		return math.Log(x), nil // 自然对数
	case "log":
		return math.Log10(x), nil // 常用对数
	default:
		return 0, errInvalid
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
