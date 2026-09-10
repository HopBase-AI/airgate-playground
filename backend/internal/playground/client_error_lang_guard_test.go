package playground

// 2026-09-10：一位西语客户在网关上收到了中文报错。AI Chat 同样是多语言产品，凡是能到达
// 终端用户的字符串（/chat/completions 错误体 message、SSE error 帧、直接流给用户的
// TerminalMessage、REST 响应体 error 字段、以及会被 err.Error() 回放进上述位置的
// fmt.Errorf/errors.New 文案）一律必须是英文；本地化在插件自己的前端按 error.code 完成
// （web/src/playground/chatErrors.ts 自带五语字典）。
//
// 本测试用 go/ast 扫描包内所有非测试源文件，四种形态任意一种带汉字即失败：
//  (i)   汉字字面量作为客户可见 emitter 的实参（含嵌套 fmt.Sprintf 与字符串拼接）；
//  (ii)  复合字面量里 message/Message/error/TerminalMessage/msg/reason/ForModel 字段带汉字；
//  (iii) 汉字被赋给 msg/message/reason/text 变量，或赋给任何随后被传进 emitter 的常量/变量；
//  (iv)  已英文化文件里的 fmt.Errorf / errors.New 带汉字。
//
// 明确豁免（只面向运营/管理员，或本就不是回给客户的文案）：
//   - metadata.go 整个文件：后台插件配置表单的 Label/Description/Placeholder，只在管理员控制台出现；
//   - BuildPluginInfo：同上；
//   - slog 日志（Debug/Info/Warn/Error）与 Go 注释：只进日志与源码；
//   - memberGroupForbiddenHints：匹配 core 内部错误文本的判别串，必须与 core 保持一致，
//     英文化会让"成员分组白名单拒绝"退化成通用 upstream_error（见 routes.go 注释）。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// clientFacingCalls：这些函数的任何字符串字面量实参都会直接进入客户可见文本。
var clientFacingCalls = map[string]bool{
	"writeOpenAIError":        true,
	"writeJSON":               true,
	"writeSSEErrorFrame":      true,
	"writeOpenAIContentDelta": true,
	"w.Write":                 true, // routes.go 里手写的 SSE error 帧
}

// clientFacingFields：复合字面量里这些字段承载的是客户/模型可见文案。
// ForModel 是工具结果正文，会被模型原样引用给用户，同样必须英文。
var clientFacingFields = map[string]bool{
	"message":         true,
	"Message":         true,
	"error":           true,
	"Error":           true,
	"msg":             true,
	"reason":          true,
	"Reason":          true,
	"TerminalMessage": true,
	"ForModel":        true,
}

// clientFacingVarNames：这些名字的变量/常量一旦带汉字，几乎必然被喂给 emitter。
var clientFacingVarNames = map[string]bool{
	"msg":     true,
	"message": true,
	"reason":  true,
	"text":    true,
}

// fmt.Errorf / errors.New 的文案最终会经 err.Error() 回放进客户可见位置，因此**全量**
// 检查——不再维护「哪些文件要查」的白名单。白名单是 opt-in 的，新建的文件默认不设防，
// 加一个新 handler 就能悄悄漏一句中文出去；这里改成 fail-closed：所有被扫描的文件都查，
// 只有 adminOnlyFiles 整体豁免。

// adminOnlyFiles / isAdminOnlyFunc：只面向后台管理员的文案，不经 API 回放给终端用户。
var adminOnlyFiles = map[string]bool{
	"metadata.go": true,
}

func isAdminOnlyFunc(name string) bool {
	return name == "BuildPluginInfo"
}

// allowedHanIdents：明确豁免的汉字常量（匹配 core 内部错误文本用，不回放给客户）。
var allowedHanIdents = map[string]bool{
	"memberGroupForbiddenHints": true,
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func TestClientFacingStringsMustBeEnglish(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if adminOnlyFiles[name] {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		violations = append(violations, scanFileForHan(fset, file)...)
	}
	if len(violations) > 0 {
		t.Fatalf("client-facing strings must be English (localization happens in web/src/playground/chatErrors.ts by error code):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func scanFileForHan(fset *token.FileSet, file *ast.File) []string {
	var violations []string
	report := func(node ast.Node, why string) {
		violations = append(violations, fset.Position(node.Pos()).String()+": "+why)
	}

	// 第一遍：收集"值里带汉字"的标识符（const/var），用于形态 (iii) 的数据流判定。
	hanIdents := collectHanIdents(file)

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && isAdminOnlyFunc(fn.Name.Name) {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				callee := calleeName(node.Fun)
				isErrorCtor := callee == "fmt.Errorf" || callee == "errors.New"
				if !clientFacingCalls[callee] && !isErrorCtor {
					return true
				}
				for _, arg := range node.Args {
					// (i)/(iv) 直接字面量（含嵌套 Sprintf 与 a + b 拼接）。
					for _, lit := range stringLiterals(arg) {
						if containsHan(lit.value) {
							report(lit.node, "Han text passed to "+callee+": "+strconv.Quote(lit.value))
						}
					}
					// (iii) 传进 emitter 的标识符，其声明值带汉字。
					for _, id := range identifiers(arg) {
						if hanIdents[id.Name] && !allowedHanIdents[id.Name] {
							report(id, "Han-valued identifier "+id.Name+" passed to "+callee)
						}
					}
				}
			case *ast.KeyValueExpr:
				key := keyName(node.Key)
				if !clientFacingFields[key] {
					return true
				}
				for _, lit := range stringLiterals(node.Value) {
					if containsHan(lit.value) {
						report(lit.node, "Han text in field "+key+": "+strconv.Quote(lit.value))
					}
				}
			case *ast.AssignStmt:
				// (iii) 汉字赋给 msg/message/reason/text。
				for i, lhs := range node.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || !clientFacingVarNames[id.Name] || i >= len(node.Rhs) {
						continue
					}
					for _, lit := range stringLiterals(node.Rhs[i]) {
						if containsHan(lit.value) {
							report(lit.node, "Han text assigned to "+id.Name+": "+strconv.Quote(lit.value))
						}
					}
				}
			case *ast.ValueSpec:
				for i, id := range node.Names {
					if !clientFacingVarNames[id.Name] || i >= len(node.Values) {
						continue
					}
					for _, lit := range stringLiterals(node.Values[i]) {
						if containsHan(lit.value) {
							report(lit.node, "Han text bound to "+id.Name+": "+strconv.Quote(lit.value))
						}
					}
				}
			}
			return true
		})
	}
	return violations
}

// collectHanIdents 收集"取值会带汉字"的标识符：
//   - 声明值直接含汉字字面量的 const/var（含函数内的 := 与 var）；
//   - 直接 return 汉字字符串的函数名（形态 b：把汉字藏进 helper 再喂给 emitter）；
//   - 以及从上面两类**多跳**传递过来的变量（形态 a：常量 → 局部变量 → emitter）。
//
// 传递用不动点迭代，直到没有新的标识符被污染为止。allowedHanIdents 里的标识符不作为
// 传播源——它们是刻意保留的中文（判别串 / 存量数据字面值），不该把下游变量也染红。
func collectHanIdents(file *ast.File) map[string]bool {
	out := map[string]bool{}

	// 形态 (b)：func f() string { return "中文" }。只看 return 表达式里**直接**可见的
	// 字符串（含拼接与 fmt.Sprintf），不下钻复合字面量——否则内容目录类文件（如
	// studio 的 inspirations.go）会被整片误判。
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, res := range ret.Results {
				for _, lit := range directStringLiterals(res) {
					if containsHan(lit.value) {
						out[fn.Name.Name] = true
					}
				}
			}
			return true
		})
	}

	// 形态 (a) 的种子：声明值直接含汉字。
	assignments := collectAssignments(file)
	for _, a := range assignments {
		for _, lit := range stringLiterals(a.rhs) {
			if containsHan(lit.value) {
				out[a.name] = true
			}
		}
	}

	// 形态 (a) 的传播：RHS 引用了已被污染的标识符 → LHS 同样被污染。不动点迭代。
	for changed := true; changed; {
		changed = false
		for _, a := range assignments {
			if out[a.name] {
				continue
			}
			for _, id := range identifiers(a.rhs) {
				if out[id.Name] && !allowedHanIdents[id.Name] {
					out[a.name] = true
					changed = true
					break
				}
			}
		}
	}
	return out
}

type assignment struct {
	name string
	rhs  ast.Expr
}

// collectAssignments 收集所有"标识符 = 表达式"（const/var 声明与函数内的 := / =）。
func collectAssignments(file *ast.File) []assignment {
	var out []assignment
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			for i, id := range node.Names {
				if i < len(node.Values) {
					out = append(out, assignment{name: id.Name, rhs: node.Values[i]})
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && i < len(node.Rhs) {
					out = append(out, assignment{name: id.Name, rhs: node.Rhs[i]})
				}
			}
		}
		return true
	})
	return out
}

// directStringLiterals 只取表达式里**直接**构成字符串值的字面量：字面量本身、括号、
// a + b 拼接、以及 fmt.Sprintf/Errorf 之类调用的实参。刻意不下钻复合字面量
// （[]T{...} / map[...]{...} / T{...}），那是数据而不是这个表达式的返回文案。
func directStringLiterals(expr ast.Expr) []stringLiteral {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			value, err := strconv.Unquote(e.Value)
			if err != nil {
				value = e.Value
			}
			return []stringLiteral{{node: e, value: value}}
		}
	case *ast.ParenExpr:
		return directStringLiterals(e.X)
	case *ast.BinaryExpr:
		return append(directStringLiterals(e.X), directStringLiterals(e.Y)...)
	case *ast.CallExpr:
		var out []stringLiteral
		for _, arg := range e.Args {
			out = append(out, directStringLiterals(arg)...)
		}
		return out
	}
	return nil
}

func calleeName(expr ast.Expr) string {
	switch fn := expr.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	}
	return ""
}

// keyName 同时认结构体字段名（Ident）与 map 字面量的字符串键（BasicLit）。
func keyName(expr ast.Expr) string {
	switch key := expr.(type) {
	case *ast.Ident:
		return key.Name
	case *ast.BasicLit:
		if key.Kind == token.STRING {
			if v, err := strconv.Unquote(key.Value); err == nil {
				return v
			}
		}
	}
	return ""
}

type stringLiteral struct {
	node  ast.Node
	value string
}

// stringLiterals 展开表达式里直接可见的字符串字面量（含 a + b 拼接与 fmt.Sprintf 的格式串）。
func stringLiterals(expr ast.Expr) []stringLiteral {
	var out []stringLiteral
	ast.Inspect(expr, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			value = lit.Value
		}
		out = append(out, stringLiteral{node: lit, value: value})
		return true
	})
	return out
}

// identifiers 取表达式里出现的标识符（用于判定"汉字常量被传进 emitter"）。
func identifiers(expr ast.Expr) []*ast.Ident {
	var out []*ast.Ident
	ast.Inspect(expr, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			// 只取选择器右侧之外的部分，避免把包名当成本地标识符。
			ast.Inspect(sel.X, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					out = append(out, id)
				}
				return true
			})
			return false
		}
		if id, ok := n.(*ast.Ident); ok {
			out = append(out, id)
		}
		return true
	})
	return out
}
