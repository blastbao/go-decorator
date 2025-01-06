package main

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/dengsgo/go-decorator/cmd/logs"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	msgLintArgsNotFound = "lint arg key not found: "
	msgLintTypeNotMatch = "lint key '%s' type not match: want %s but got %s"
	msgLint
)

var (
	errUsedDecorSyntaxErrorLossFunc  = errors.New("syntax error using decorator: miss decorator name")
	errUsedDecorSyntaxErrorLossValue = errors.New("syntax error using decorator: miss parameters value")
	errUsedDecorSyntaxErrorInvalidP  = errors.New("syntax error using decorator: invalid parameter format")
	errUsedDecorSyntaxError          = errors.New("syntax error using decorator")
	errCalledDecorNotDecorator       = errors.New("used decor is not a decorator function")

	errLintSyntaxError = errors.New("syntax error using go:decor-lint")
)

type linterCheckError struct {
	msg string
	pos token.Pos
}

func newLinterCheckError(msg string, pos token.Pos) *linterCheckError {
	return &linterCheckError{
		msg: msg,
		pos: pos,
	}
}

func (l *linterCheckError) Error() string {
	return l.msg
}

// 判断一个函数声明（FuncDecl）是否是装饰器函数。
// 装饰器函数有特定的签名，比如接收一个特定类型（通常是 *Context）的参数，并且不返回值。
func isDecoratorFunc(fd *ast.FuncDecl, pkgName string) bool {
	if pkgName == "" || // 包名为空
		fd == nil || // 非函数声明
		fd.Recv != nil || // 有接收者
		fd.Type == nil || // 没有类型信息
		fd.Type.Params == nil || // 没有参数列表
		fd.Type.Params.NumFields() != 1 || // 参数列表包含多个参数
		fd.Type.Params.List[0] == nil || // 第一个参数为空
		fd.Type.Params.List[0].Type == nil { // 第一个参数类型为空
		return false
	}

	// 取第一个参数类型
	expr := fd.Type.Params.List[0].Type
	// 将参数类型信息打印到缓冲区 buffer 中
	buffer := bytes.NewBuffer([]byte{})
	err := printer.Fprint(buffer, emptyFset, expr)
	if err != nil {
		logs.Debug("funIsDecorator printer.Fprint fail", err)
		return false
	}
	// 比较 buffer.String() 与期望的字符串（*pkgName.Context）是否相同
	return strings.TrimSpace(buffer.String()) == fmt.Sprintf("*%s.Context", pkgName)
}

func parseDecorAndParameters(s string) (string, map[string]string, error) {
	// s like:
	//   function
	//   function#{}
	//   function#{key:""}
	//   function#{key:"", name:""}
	//   function#{key:"", name:"", age:100}
	//   function#{key:"", name:"", age:100, b: false}
	if s == "" {
		return "", nil, errUsedDecorSyntaxErrorLossFunc
	}

	// 通过 # 将字符串 s 分割为两部分：
	//  - _callName：函数的名称部分。
	//	- pStr：装饰器的参数部分，如果没有 # 则 pStr 为空字符串。
	_callName, pStr, _ := strings.Cut(s, "#")

	// 解析函数名称 _callName 得到选择表达式 *ast.SelectorExpr 或标识符 *ast.Ident ，再将其从 ast 转换为字符串。
	cAst, err := parser.ParseExpr(_callName)
	if err != nil {
		return "", nil, errUsedDecorSyntaxError
	}
	callName := ""
	switch a := cAst.(type) {
	case *ast.SelectorExpr, *ast.Ident:
		callName = typeString(a) // 从 ast 转换为字符串
	default:
		return "", nil, errUsedDecorSyntaxError
	}
	if callName == "" { // non
		return callName, nil, errUsedDecorSyntaxErrorLossFunc
	}

	// 解析参数
	p := newMapV[string, string]() // 存储解析后的参数
	pStr = strings.TrimSpace(pStr)
	if pStr == "" {
		if strings.HasSuffix(s, "#") {
			// 语法错误
			return callName, p.items, errUsedDecorSyntaxError
		}
		// 参数为空
		return callName, p.items, nil
	}
	// 检查 pStr 是否以 { 开头并以 } 结尾，这是参数部分的基本格式。如果不符合要求，返回解析错误。
	if pStr[0] != '{' || pStr[len(pStr)-1] != '}' {
		return callName, nil, errUsedDecorSyntaxError
	}
	// 如果 pStr 长度为 2（即 {}），表示没有任何参数，直接返回空的参数映射。
	if len(pStr) == 2 {
		// {}
		return callName, p.items, nil
	}
	// 如果 pStr 的长度小于 5，说明参数部分的格式有问题，返回错误。
	if len(pStr) < 5 {
		return callName, p.items, errUsedDecorSyntaxError
	}

	// 解析参数字符串 pStr，将其转换为 []ast.Expr
	exprList, err := parseDecorParameterStringToExprList(pStr)
	if err != nil {
		return callName, p.items, err
	}
	// 将解析后的表达式列表 exprList 转换为参数映射（p.items）
	if err := decorStmtListToMap(exprList, p); err != nil {
		return callName, p.items, err
	}
	return callName, p.items, nil
}

// ast.BasicLit
//	用途：表示基本字面量的值，即程序中的常量。
//	类型：常用于表示整数、浮点数、字符串、字符等字面量。
//	属性：
//		Kind: 字面量的类型（如 token.INT, token.FLOAT, token.STRING 等）。
//		Value: 字面量的值，以字符串形式存储。
//	示例
//		literal := &ast.BasicLit{
//			Value: `"Hello, world!"`,  // 字符串字面量
//			Kind:  token.STRING,
//		}
//		literalInt := &ast.BasicLit{
//			Value: "123",  // 整数字面量
//			Kind:  token.INT,
//		}
//		literalFloat := &ast.BasicLit{
//			Value: "3.14",  // 浮点数字面量
//			Kind:  token.FLOAT,
//		}
//
// ast.Ident
//	用途：表示标识符。
//	类型：用于变量名、函数名、类型名等。
//	属性：
//		Name: 标识符的名称。
//		Obj: （可选）指向相关对象的引用（如变量、函数等）。
//	示例：
//		identifier := &ast.Ident{
//			Name: "x",  // 标识符名称
//		}
//		identifierFunc := &ast.Ident{
//			Name: "Print",  // 函数名
//		}
//

// 将 AST 表达式列表解析为键值对映射，存储在 mapV[string, string] 中。
//
// 参数说明：
//   - exprList：一个 []ast.Expr 类型的表达式列表，表示参数的键值对。每个元素通常是一个 *ast.KeyValueExpr 类型的表达式，即 key: value 形式。
//   - p：一个 mapV[string, string] 类型的字典，表示解析后存储的键值对。
//
// 例子：
//
//	exprList := []ast.Expr{
//	   &ast.KeyValueExpr{
//	       Key:   ast.NewIdent("a"),
//	       Value: &ast.BasicLit{Kind: token.STRING, Value: `"b"`},
//	   },
//	   &ast.KeyValueExpr{
//	       Key:   ast.NewIdent("c"),
//	       Value: &ast.BasicLit{Kind: token.INT, Value: "100"},
//	   },
//	   &ast.KeyValueExpr{
//	       Key:   ast.NewIdent("flag"),
//	       Value: ast.NewIdent("true"),
//	   },
//	}
//
//	==>
//
//	p := mapV[string, string]{
//	   "a":    "b",
//	   "c":    "100",
//	   "flag": "true",
//	}
func decorStmtListToMap(exprList []ast.Expr, p *mapV[string, string]) error {
	// 从 ast.Expr 类型的表达式中提取出标识符（*ast.Ident）的名称。
	ident := func(v ast.Expr) string {
		if v == nil {
			return ""
		}
		id, ok := v.(*ast.Ident)
		if !ok {
			return ""
		}
		return id.Name
	}

	// 处理每个 *ast.KeyValueExpr 类型的表达式，提取键和值，并根据值的类型进行不同的处理：
	//	- 如果值是基本字面量（*ast.BasicLit 或 *ast.UnaryExpr），则判断其类型是否为 string、int 或 float，并将其值存入字典 p 中。
	//	- 如果值是 *ast.Ident（标识符），则检查该值是否为 true 或 false，并将其值存入字典 p 中。
	//	- 如果出现重复的键或无效的值类型，将返回错误。
	consumerKeyValue := func(expr *ast.KeyValueExpr) error {
		key := ident(expr.Key)
		if key == "" {
			return errors.New("invalid parameter name") // error
		}
		switch value := expr.Value.(type) {
		case *ast.BasicLit, *ast.UnaryExpr: // 基础字面量或者一元表达式
			val := realBasicLit(value)
			if val == nil {
				return errors.New("invalid parameters value, key '" + key + "'")
			}
			switch val.Kind {
			// a:"b"
			// a: 0
			// a: 0.0
			case token.STRING, token.INT, token.FLOAT:
				if !p.put(key, val.Value) {
					return errors.New("duplicate parameters key '" + key + "'")
				}
			default:
				return errors.New("invalid parameter type") // error
			}
		case *ast.Ident: // 标识符
			val := ident(value)
			if val != "true" && val != "false" {
				return errors.New("invalid parameter value, should be bool")
			}
			if !p.put(key, val) {
				return errors.New("duplicate parameters key '" + key + "'")
			}
		default:
			return errors.New("invalid parameter value")
		}
		return nil
	}

	// 遍历每个表达式，如果是 *ast.KeyValueExpr 类型，则调用 consumerKeyValue 函数来处理。
	// 如果遇到其他类型的表达式，返回 errUsedDecorSyntaxErrorInvalidP 错误。
	for _, v := range exprList {
		switch expr := v.(type) {
		case *ast.KeyValueExpr: // a:b
			if err := consumerKeyValue(expr); err != nil {
				return err
			}
		default:
			return errUsedDecorSyntaxErrorInvalidP // error
		}
	}

	return nil // error
}

// s = {xxxxx}
//
// 示例 1：基本的装饰器参数
//
//	输入："{key: 1, name: \"test\"}"
//	解析过程：
//		在输入字符串前加上 "map[any]any"，得到："map[any]any{key: 1, name: \"test\"}"
//		解析后得到一个复合字面量：map[any]any{key: 1, name: "test"}
//		这是一个 map 类型的复合字面量，返回的 clit.Elts 是一个元素列表：
//			[]ast.Expr{
//	   			&ast.KeyValueExpr{
//	       			Key:   &ast.Ident{Name: "key"},
//	       			Value: &ast.BasicLit{Value: "1", Kind: token.INT},
//	   			},
//	   			&ast.KeyValueExpr{
//	       			Key:   &ast.Ident{Name: "name"},
//	       			Value: &ast.BasicLit{Value: "\"test\"", Kind: token.STRING},
//	   			},
//			}
//
// 示例 2：没有元素的装饰器参数（空 {}）
//
//	输入： "{}"
//	解析过程：
//		输入字符串 "{}" 被加上前缀 "map[any]any"，变成："map[any]any{}"
//		解析后得到一个空的 map 复合字面量：map[any]any{}
//		这是一个没有元素的 map 类型的复合字面量，返回的 clit.Elts 是一个空的元素列表。
//			[]ast.Expr{}
//
// 示例 3：带有复杂类型的装饰器参数
//
//	输入："{key: 1, name: \"test\", age: 30}"
//	解析过程：
//		加上前缀 "map[any]any"，变成："map[any]any{key: 1, name: \"test\", age: 30}"
//		解析后得到一个 map 类型的复合字面量：map[any]any{key: 1, name: "test", age: 30}
//		返回的 clit.Elts 是一个元素列表，包含这三个表达式：
//			[]ast.Expr{
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "key"},
//				   Value: &ast.BasicLit{Value: "1", Kind: token.INT},
//			   },
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "name"},
//				   Value: &ast.BasicLit{Value: "\"test\"", Kind: token.STRING},
//			   },
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "age"},
//				   Value: &ast.BasicLit{Value: "30", Kind: token.INT},
//			   },
//			}
//
// 示例 4：带有布尔值的装饰器参数
//
//	输入："{enabled: true, debug: false}"
//	解析过程：
//		加上前缀 "map[any]any"，变成："map[any]any{enabled: true, debug: false}"
//		解析后得到一个 map 类型的复合字面量：map[any]any{enabled: true, debug: false}
//		返回的 clit.Elts 是一个元素列表：
//			[]ast.Expr{
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "enabled"},
//				   Value: &ast.BasicLit{Value: "true", Kind: token.BOOL},
//			   },
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "debug"},
//				   Value: &ast.BasicLit{Value: "false", Kind: token.BOOL},
//			   },
//			}
//
// 示例 5：无效的装饰器参数（格式错误）
// 输入："key: 1, name: \"test\""
// 解析过程：输入字符串 key: 1, name: "test" 没有被正确包裹在 {} 中，也没有前缀 "map[any]any"。
// 由于语法不正确，函数会返回解析错误 errUsedDecorSyntaxErrorInvalidP 。
//
// 示例 6：只有值没有键的装饰器参数（无 key，只有 value）
//
//	输入："{1, 2, 3}"
//	解析过程：
//		加上前缀 "map[any]any"，变成："map[any]any{1, 2, 3}"
//		解析后得到一个 map 类型的复合字面量：map[any]any{1, 2, 3}
//		这是一个包含值但没有键的复合字面量。由于没有键，clit.Elts 会包含每个值作为单独的 ast.Expr，这里的值会作为 *ast.BasicLit（基本字面量）。
//		返回的 clit.Elts 是：
//			[]ast.Expr{
//			   &ast.BasicLit{Value: "1", Kind: token.INT},
//			   &ast.BasicLit{Value: "2", Kind: token.INT},
//			   &ast.BasicLit{Value: "3", Kind: token.INT},
//			}
//
// 示例 7：包含数组或切片作为值的装饰器参数
//
//	输入："{names: [\"Alice\", \"Bob\", \"Charlie\"]}"
//	解析过程：
//		输入字符串加上前缀 "map[any]any"，变成："map[any]any{names: [\"Alice\", \"Bob\", \"Charlie\"]}"
//		解析后得到一个包含数组/切片的 map 类型复合字面量：map[any]any{names: ["Alice", "Bob", "Charlie"]}
//		这是一个包含数组的 key 为 names 的 KeyValueExpr，值为一个数组。数组被解析为 *ast.ArrayType 和其中的 *ast.BasicLit（基本字面量）。
//		返回的 clit.Elts 会包含以下表达式：
//			[]ast.Expr{
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "names"},
//				   Value: &ast.ArrayType{
//					   Len: nil,
//					   Ellipsis: false,
//					   Elem: &ast.Ident{Name: "string"},
//					   Elts: []ast.Expr{
//						   &ast.BasicLit{Value: "\"Alice\"", Kind: token.STRING},
//						   &ast.BasicLit{Value: "\"Bob\"", Kind: token.STRING},
//						   &ast.BasicLit{Value: "\"Charlie\"", Kind: token.STRING},
//					   },
//				   },
//			   },
//			}
//
// 示例 8：包含函数或方法调用作为值的装饰器参数
//
//	输入："{action: logError()}"
//	解析过程：
//		输入字符串加上前缀 "map[any]any"，变成："map[any]any{action: logError()}"
//		解析后得到一个包含函数调用的 map 类型复合字面量：map[any]any{action: logError()}
//		这里的 action 键对应一个函数调用 logError()，它被解析为 *ast.CallExpr（调用表达式）。
//		返回的 clit.Elts 会包含：
//			[]ast.Expr{
//			   &ast.KeyValueExpr{
//				   Key:   &ast.Ident{Name: "action"},
//				   Value: &ast.CallExpr{
//					   Fun: &ast.Ident{Name: "logError"},
//					   Args: []ast.Expr{},
//				   },
//			   },
//			}
//
// 示例 9：包含结构体字面量作为值的装饰器参数
//
//	输入："{config: {timeout: 30, retries: 3}}"
//	解析过程：
//		输入字符串加上前缀 "map[any]any"，变成："map[any]any{config: {timeout: 30, retries: 3}}"
//		解析后得到一个包含结构体字面量的 map 类型复合字面量：map[any]any{config: {timeout: 30, retries: 3}}
//		这里的 config 键对应一个结构体字面量，它被解析为 *ast.CompositeLit，包含 timeout 和 retries 两个字段。
//		返回的 clit.Elts 会包含：
//			[]ast.Expr{
//			   &ast.KeyValueExpr{
//				   Key: &ast.Ident{Name: "config"},
//				   Value: &ast.CompositeLit{
//					   Type: &ast.Ident{Name: "Config"},
//					   Elts: []ast.Expr{
//						   &ast.KeyValueExpr{
//							   Key:   &ast.Ident{Name: "timeout"},
//							   Value: &ast.BasicLit{Value: "30", Kind: token.INT},
//						   },
//						   &ast.KeyValueExpr{
//							   Key:   &ast.Ident{Name: "retries"},
//							   Value: &ast.BasicLit{Value: "3", Kind: token.INT},
//						   },
//					   },
//				   },
//			   },
//			}
func parseDecorParameterStringToExprList(s string) ([]ast.Expr, error) {
	s = "map[any]any" + s
	stmts, _, err := getStmtList(s)
	if err != nil {
		return nil, errUsedDecorSyntaxErrorInvalidP
	}
	if len(stmts) != 1 {
		return nil, errUsedDecorSyntaxErrorInvalidP
	}

	stmt, ok := stmts[0].(*ast.ExprStmt)
	if !ok || stmt == nil {
		return nil, errUsedDecorSyntaxErrorInvalidP
	}

	clit, ok := stmt.X.(*ast.CompositeLit)
	if !ok || clit == nil {
		return nil, errUsedDecorSyntaxErrorInvalidP
	}

	if clit.Elts == nil {
		return nil, errUsedDecorSyntaxErrorInvalidP
	}

	return clit.Elts, nil
}

func checkDecorAndGetParam(pkgPath, funName string, annotationMap map[string]string) ([]string, error) {
	// 查找指定包路径（pkgPath）中的函数 funName 的声明（decl）
	fset, decl, file, err := pkgILoader.findFunc(pkgPath, funName)
	if err != nil {
		return nil, err
	}

	// 创建一个新的导入器，并尝试从文件中提取装饰器包的导入路径。
	imp := newImporter(file)
	pkgName, ok := imp.importedPath(decoratorPackagePath)
	if !ok {
		return nil, errors.New(msgDecorPkgNotFound)
	}

	// 将 funName 的声明中的参数列表转换为 map
	m := collDeclFuncParamsAnfTypes(decl)
	if len(m) < 1 {
		return nil, errCalledDecorNotDecorator
	}

	// 检查第一个参数是否为 *xxx.Context
	for _, v := range m {
		if v.index == 0 && v.typ != fmt.Sprintf("*%s.Context", pkgName) {
			return nil, errors.New("used decor is not a decorator function")
		}
	}

	if len(m) == 1 {
		return []string{}, nil
	}
	if err := parseLinterFromDocGroup(decl.Doc, m); err != nil {
		return nil, errors.New(fmt.Sprintf("%s\n\tLint: %s", err.Error(), friendlyIDEPosition(fset, err.pos)))
	}

	params := make([]string, len(m))
	for _, v := range m {
		// 跳过第一个参数
		if v.index == 0 {
			continue
		}
		if value, ok := annotationMap[v.name]; ok {
			// 检查：如果 v.nonzero 为 true，则要求 value 不能为零，否则报错；
			if err := v.passNonzeroLint(value); err != nil {
				return nil, err
			}
			// 检查：检查 value 是否是合法枚举、合法取值区间
			if err := v.passRequiredLint(value); err != nil {
				return nil, err
			}
			// 通过检查，保存到 params 中
			params[v.index] = value
		} else {
			// 如果 value 不存在，检查该参数是否运行为空，不许则报错
			if v.nonzero {
				return nil, errors.New(fmt.Sprintf("lint: key '%s' can't pass nonzero lint, must have value", v.name))
			}
			// 根据参数类型设置默认值
			switch v.typeKind() {
			case types.IsInteger:
				params[v.index] = "0"
			case types.IsFloat:
				params[v.index] = "0.0"
			case types.IsString:
				params[v.index] = `""`
			case types.IsBoolean:
				params[v.index] = "false"
			default:
				return nil, errors.New("unsupported types '" + v.typ + "'")
			}
		}
	}

	//go:decor logging#(key : "")   func(key, name, instance string)
	return params[1:], nil
}

// Go 语言的 ast.CommentGroup 表示一组注释，可能包含多个注释行。
func parseLinterFromDocGroup(doc *ast.CommentGroup, args decorArgsMap) *linterCheckError {
	// 检查注释是否为空。
	if doc == nil || doc.List == nil || len(doc.List) == 0 {
		return nil
	}
	// 从后向前遍历注释
	for i := len(doc.List) - 1; i >= 0; i-- {
		comment := doc.List[i]
		// 检查注释是否以指定的标志开头
		if !strings.HasPrefix(comment.Text, decorLintScanFlag) {
			break
		}
		// 解析注释的剩余部分，过程中会填充 args 中的字段信息
		if err := resolveLinterFromAnnotation(comment.Text[len(decorLintScanFlag):], args); err != nil {
			return newLinterCheckError(err.Error(), comment.Pos())
		}
	}
	return nil
}

func resolveLinterFromAnnotation(s string, args decorArgsMap) error {
	switch {
	case strings.HasPrefix(s, "required: "):
		exprList, err := parseDecorParameterStringToExprList(strings.TrimLeft(s, "required: "))
		if err != nil {
			return errLintSyntaxError
		}
		for _, v := range exprList {
			if err := obtainRequiredLinter(v, args); err != nil {
				return err
			}
		}
	case strings.HasPrefix(s, "nonzero: "):
		exprList, err := parseDecorParameterStringToExprList(strings.TrimLeft(s, "nonzero: "))
		if err != nil {
			return errLintSyntaxError
		}
		for _, v := range exprList {
			// 检查 v 是否非空？若非空设置 args[v].nonzero = true ，否则报错。
			if err := obtainNonzeroLinter(v, args); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid linter: " + s)
	}
	return nil
}

func obtainRequiredLinter(v ast.Expr, args decorArgsMap) error {
	// 初始化 decorArg 结构中的 required 字段
	initRequiredLinter := func(v *decorArg) {
		if v.required != nil {
			return
		}
		v.required = &requiredLinter{}
	}

	// 处理不同类型的 ast.Expr 表达式
	switch expr := v.(type) {
	case *ast.Ident: // {a}
		// 如果 v 是一个标识符（例如 {a}），尝试从 args 中查找与 expr.Name（标识符名）对应的 decorArg 。

		// 从 args 中查找与 expr.Name（标识符名）对应的 decorArg ，找不到报错
		dpt, ok := args[expr.Name]
		if !ok {
			return errors.New(msgLintArgsNotFound + expr.Name) // error
		}
		// 如果找到，初始化其 required 字段。
		initRequiredLinter(dpt)
	case *ast.KeyValueExpr: // {a:{}}
		// 如果 v 是一个键值对表达式（例如 {a: {}}）

		// 检查键（expr.Key）是否是一个标识符
		if _, ok := expr.Key.(*ast.Ident); !ok {
			return errLintSyntaxError
		}
		// 尝试在 args 中找到与该标识符名匹配的 decorArg
		dpt, ok := args[expr.Key.(*ast.Ident).Name]
		if !ok {
			return errors.New(msgLintArgsNotFound + expr.Key.(*ast.Ident).Name)
		}
		// 检查值是否是复合字面量
		if _, ok := expr.Value.(*ast.CompositeLit); !ok {
			return errLintSyntaxError
		}

		for _, lit := range expr.Value.(*ast.CompositeLit).Elts {
			switch lit := lit.(type) {
			case *ast.BasicLit, *ast.UnaryExpr: // {a:{"", "", 1, -1}}
				// 基本字面量和一元表达式

				// 解析基础字面量
				rlit := realBasicLit(lit)
				if rlit == nil {
					return errLintSyntaxError
				}
				// 类型匹配检查
				if (rlit.Kind == token.STRING && dpt.typeKind() != types.IsString) ||
					(rlit.Kind == token.INT && dpt.typeKind() != types.IsInteger) ||
					(rlit.Kind == token.FLOAT && dpt.typeKind() != types.IsFloat) {
					return errors.New(fmt.Sprintf(msgLintTypeNotMatch, dpt.name, dpt.typ, rlit.Kind.String()))
				}
				// 将字面量添加到 requiredLinter 的 enum 字段中。
				initRequiredLinter(dpt)
				if dpt.required.enum == nil {
					dpt.required.enum = []string{}
				}
				dpt.required.enum = append(dpt.required.enum, rlit.Value)
			case *ast.Ident: // {a:{true, false}}
				// 标识符

				// 只支持 true 或 false
				if lit.Name != "true" && lit.Name != "false" {
					return errors.New(fmt.Sprintf("lint required key '%s' value must be true or false, but got %s", dpt.name, lit.Name))
				}
				// 将 true 或 false 添加到 requiredLinter 的 enum 字段中。
				initRequiredLinter(dpt)
				if dpt.required.enum == nil {
					dpt.required.enum = []string{}
				}
				dpt.required.enum = append(dpt.required.enum, lit.Name)
			case *ast.KeyValueExpr: // {a:{gte:1.0, lte:1.0}}
				// 获取 key 标识符
				if _, ok := lit.Key.(*ast.Ident); !ok {
					return errLintSyntaxError
				}
				key := lit.Key.(*ast.Ident).Name

				// 如果参数是布尔类型，使用比较操作（如 gte, lte）会返回错误，因为布尔类型不具备进行数值比较的意义
				if dpt.typeKind() == types.IsBoolean {
					return errors.New(fmt.Sprintf("lint required key '%s' can't use %s compare", dpt.name, key))
				}

				// 检查 key 标识符是否是 ge/gte/le/lte
				if _, ok := lintRequiredRangeAllowKeyMap[key]; !ok {
					return errors.New(fmt.Sprintf("lint required key '%s' not allow %s", dpt.name, key))
				}

				// 读取 value 字面量
				lity := realBasicLit(lit.Value)
				if lity == nil {
					return errLintSyntaxError
				}
				if lity.Kind != token.FLOAT && lity.Kind != token.INT {
					return errors.New(fmt.Sprintf("lint required key '%s' compare %s must be int or float, but got %s", dpt.name, key, lity.Kind.String()))
				}

				initRequiredLinter(dpt)
				dpt.required.initCompare()
				var err error
				dpt.required.compare[key], err = strconv.ParseFloat(lity.Value, 64)
				if err != nil {
					return errors.New(fmt.Sprintf("lint required key '%s' compare %s value canot be convert to float, %s; error: %+v", dpt.name, key, lity.Value, err))
				}

			default:
				return errLintSyntaxError
			}
		}

	default:
		return errLintSyntaxError
	}

	return nil
}

// 检查 v 是否非空？若非空设置标记否则报错。
// - 如果 v 是一个标识符（*ast.Ident），获取其名称。
// - 在 args 中查找该名称对应的值。
// - 如果找不到，返回错误 msgLintArgsNotFound 。
// - 如果找到，将对应的 dpt.nonzero 标记为 true 。
func obtainNonzeroLinter(v ast.Expr, args decorArgsMap) error {
	switch expr := v.(type) {
	case *ast.Ident: // {a}
		dpt, ok := args[expr.Name]
		if !ok {
			return errors.New(msgLintArgsNotFound + expr.Name) // error
		}
		dpt.nonzero = true
	default:
		return errLintSyntaxError
	}
	return nil
}

// 从函数声明（*ast.FuncDecl）中提取参数名和类型，并整理成一个映射（decorArgsMap）。
func collDeclFuncParamsAnfTypes(fd *ast.FuncDecl) (m decorArgsMap) {
	m = decorArgsMap{}
	if fd == nil || // 函数声明不为空
		fd.Type == nil || // 函数类型不为空
		fd.Type.Params == nil || // 参数列表不为空
		fd.Type.Params.NumFields() == 0 || // 至少有一个参数
		fd.Type.Params.List[0] == nil { // 第一个元素不空
		return m
	}
	index := 0

	// 遍历函数的所有参数
	for _, field := range fd.Type.Params.List {
		// 将参数类型转换成字符串形式
		typ := typeString(field.Type)
		// 当一个参数是多个变量时，如 x, y int ，遍历这些变量
		for _, id := range field.Names {
			m[id.Name] = &decorArg{index, id.Name, typ, nil, false}
			index++ // 每处理一个参数，index 加 1
		}
	}
	return m
}

var pkgILoader = newPkgLoader()

type pkgLoader struct {
	pkg   map[string]*pkgSet
	funcs map[string]*ast.FuncDecl
}

func newPkgLoader() *pkgLoader {
	return &pkgLoader{
		pkg:   map[string]*pkgSet{},
		funcs: map[string]*ast.FuncDecl{},
	}
}

func (d *pkgLoader) findFunc(pkgPath, funName string) (fileSet *token.FileSet, target *ast.FuncDecl, file *ast.File, err error) {
	return d.findTarget(pkgPath, funName)
}

// findTarget 从指定的路径（pkgPath）中查找并返回一个特定的函数声明（*ast.FuncDecl）。
// findTarget 还会返回该函数所在的文件集（token.FileSet）和文件对象（*ast.File）。
func (d *pkgLoader) findTarget(pkgPath string, funName string) (fileSet *token.FileSet, target *ast.FuncDecl, afile *ast.File, err error) {
	// 加载指定路径下的包信息
	set, err := d.loadPkg(pkgPath)
	if err != nil {
		return nil, nil, nil, err
	}

	// 扩展名处理？？？
	err = errors.New("decorator not found: " + pkgPath + "#" + funName)
	if ext := filepath.Ext(funName); ext != "" {
		funName = ext[1:]
	}

	//log.Printf("pkgPath: %s, funName: %s, set: %+v \n", pkgPath, funName, set)
	// 遍历所有包
	for _, v := range set.pkgs {
		if v == nil || v.Files == nil {
			continue
		}
		// 遍历包中的所有文件
		for _, file := range v.Files {
			// 遍历文件中的所有声明
			visitAstDecl(file, func(decl *ast.FuncDecl) bool {
				// 声明非空 && 名称非空 && 非成员函数 && 名称等于目标 funName
				if decl == nil || decl.Name == nil || decl.Name.Name != funName || decl.Recv != nil {
					return false
				}
				afile = file  // 保存源文件
				target = decl // 保存函数声明
				fileSet = set.fset
				err = nil
				return true // 找到、退出
			})
		}
	}
	return
}

func (d *pkgLoader) loadPkg(pkgPath string) (set *pkgSet, err error) {
	// 读取缓存
	if _set, ok := d.pkg[pkgPath]; ok {
		set = _set
		return
	}

	// 加载新包
	pi, err := getPackageInfo(pkgPath) // 获取包的基本信息
	if err != nil {
		return nil, err
	}
	set = &pkgSet{}
	set.fset = token.NewFileSet()                                                // 创建一个新的空的文件集合 token.FileSet ，用于管理源代码文件中的位置信息（例如，行号、列号等）。
	set.pkgs, err = parser.ParseDir(set.fset, pi.Dir, nil, parser.ParseComments) // 解析包的源代码目录，pi.Dir 是包的源代码路径，parser.ParseComments 表示解析时需要考虑注释。
	if err != nil {
		return nil, err
	}

	// 缓存解析结果
	d.pkg[pkgPath] = set
	return
}

// 该函数用于解析基础字面量，处理可能的符号（正负号）。
//
// 示例
//
//	输入：&ast.BasicLit{Kind: token.INT, Value: "42"}
//	输出：&ast.BasicLit{Kind: token.INT, Value: "42"}
//
//	输入：&ast.UnaryExpr{Op: token.ADD, X: &ast.BasicLit{Kind: token.FLOAT, Value: "3.14"}}
//	输出：&ast.BasicLit{Kind: token.FLOAT, Value: "3.14"}
//
//	输入：&ast.UnaryExpr{Op: token.SUB, X: &ast.BasicLit{Kind: token.INT, Value: "42"}}
//	输出：&ast.BasicLit{Kind: token.INT, Value: "-42"}
//
//	输入：其他类型的 ast.Expr
//	输出：nil
func realBasicLit(v ast.Expr) *ast.BasicLit {
	switch v := v.(type) {
	case *ast.BasicLit: // 基本字面量
		return v
	case *ast.UnaryExpr: // 一元表达式，包含一个基本字面量，操作符是加号（+）或减号（-）。
		lit, ok := v.X.(*ast.BasicLit)
		if !ok {
			return nil
		}
		if v.Op == token.ADD {
			return lit // 如果操作符是加号 (+)，直接返回这个字面量
		}
		if v.Op == token.SUB {
			lit.Value = v.Op.String() + lit.Value
			return lit // 如果操作符是减号 (-)，则在字面量的值前加上负号
		}
		return nil // 其他操作符不支持
	}
	return nil
}

// 检查一个字符串是否只包含字母。
//
// 检查给定字符串 s 中是否全部都是字母。
// 字母是指 Unicode 字符集中属于字母类别的字符，包含大小写字母（A-Z, a-z）以及其他语言中的字母（如汉字、希腊字母等）。
func isLetters(s string) (b bool) {
	for offset := 0; offset < len(s); {
		r, size := utf8.DecodeRuneInString(s[offset:])
		if r == utf8.RuneError {
			return b
		}
		offset += size
		if !unicode.IsLetter(r) {
			return false
		}
		b = true
	}
	return b
}

// 移除字符串中的所有空白字符。
//
// 空白字符包括空格、制表符、换行符、回车符等所有 Unicode 定义的空白字符。
func cleanSpaceChar(s string) string {
	bf := bytes.NewBuffer([]byte{})
	offset := 0
	for offset < len(s) {
		r, size := utf8.DecodeRuneInString(s[offset:])
		offset += size
		if unicode.IsSpace(r) {
			continue
		}
		bf.WriteRune(r)
	}
	return bf.String()
}
