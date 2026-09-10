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

// errorConstructorFiles：这些文件里的 fmt.Errorf / errors.New 文案最终经 err.Error()
// 回放给客户（writeJSON(map{"error": err.Error()}) / ForModel + err.Error() 等）。
var errorConstructorFiles = map[string]bool{
	"direct_export.go":              true,
	"host_api.go":                   true,
	"markdown_render.go":            true,
	"office_renderer.go":            true,
	"pdf_renderer.go":               true,
	"routes.go":                     true,
	"service.go":                    true,
	"tavily.go":                     true,
	"tool_generate_document.go":     true,
	"tool_generate_presentation.go": true,
	"tool_generate_spreadsheet.go":  true,
	"tool_loop.go":                  true,
	"tool_web_search.go":            true,
}

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
		violations = append(violations, scanFileForHan(fset, name, file)...)
	}
	if len(violations) > 0 {
		t.Fatalf("client-facing strings must be English (localization happens in web/src/playground/chatErrors.ts by error code):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func scanFileForHan(fset *token.FileSet, name string, file *ast.File) []string {
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
				isErrorCtor := errorConstructorFiles[name] && (callee == "fmt.Errorf" || callee == "errors.New")
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

// collectHanIdents 收集包内声明值含汉字的 const/var 名字（含函数内的 := 与 var）。
func collectHanIdents(file *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			for i, id := range node.Names {
				if i >= len(node.Values) {
					continue
				}
				for _, lit := range stringLiterals(node.Values[i]) {
					if containsHan(lit.value) {
						out[id.Name] = true
					}
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || i >= len(node.Rhs) {
					continue
				}
				for _, lit := range stringLiterals(node.Rhs[i]) {
					if containsHan(lit.value) {
						out[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return out
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
