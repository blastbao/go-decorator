package main

import (
	"bytes"
	"fmt"
	"github.com/dengsgo/go-decorator/cmd/logs"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"math/rand"
	"strconv"
	"strings"
	"text/template"
)

const randSeeds = "abcdefghijklmnopqrstuvwxyz"

var emptyFset = token.NewFileSet()

const replaceTpl = `    ${.DecorVarName} := &decor.Context{
        Kind:       decor.${.TKind},
        TargetName: ${.TargetName},
        Receiver:   ${.ReceiverVarName},
        TargetIn:   []any{${stringer .InArgNames}},
        TargetOut:  []any{${stringer .OutArgNames}},
    }
    ${.DecorVarName}.Func = func() {
        ${if .HaveReturn}${stringer .DecorListOut} = ${end}${.FuncMain} (${stringer .DecorCallIn})
    }
    ${.DecorCallName}(${.DecorVarName}${if .HaveDecorParam}, ${stringer .DecorCallParams}${end})
    ${if .HaveReturn}return ${stringer .DecorCallOut}${end}`

type ReplaceArgs struct {
	HaveDecorParam, // 是否有装饰参数，如果有需要引用 DecorCallParams
	HaveReturn bool // 是否有返回值，如果有需要引用 DecorListOut/DecorCallOut
	TKind, // target kind // 目标类型，可能是函数、方法等
	TargetName, // 目标函数或方法的名称
	ReceiverVarName, // Receiver var  // 目标函数的接收者（适用于方法）
	DecorVarName, // decor var // 装饰器变量的名称
	DecorCallName, // decor function name . logging // 装饰器调用函数的名称
	FuncMain string // (a, b, c) {raw func} // 目标函数
	DecorCallParams, // decor function parameters. like "", 0, true, options, default empty // 装饰器调用时传递的参数
	InArgNames, // a, b, c // 输入参数名
	OutArgNames, // c, d		// 输出参数名
	InArgTypes, // int, int, int // 输入参数的类型
	OutArgTypes, // int, int		// 输出参数的类型
	DecorListOut, // decor.TargetOut[0], decor.TargetOut[1] // 装饰器的输出参数
	DecorCallIn, // decor.TargetIn[0].(int), decor.TargetIn[1].(int), decor.TargetIn[2].(int) // 装饰器的输入参数
	DecorCallOut []string // decor.TargetOut[0].(int), decor.TargetOut[1].(int) // 装饰器的输出参数
}

func newReplaceArgs(gi *genIdentId, targetName, decorName string) *ReplaceArgs {
	return &ReplaceArgs{
		false,
		false,
		"KFunc",                // decor.TKind,
		`"` + targetName + `"`, // 目标名
		"nil",
		gi.nextStr(),
		decorName, // 装饰名
		"",
		[]string{},
		[]string{},
		[]string{},
		[]string{},
		[]string{},
		[]string{},
		[]string{},
		[]string{},
	}
}

func replace(args *ReplaceArgs) (string, error) {
	// 通过模板引擎将 ReplaceArgs 中的值替换到模板中的占位符位置，最终生成目标的装饰器代码。
	tpl, err := template.
		New("decorReplace").
		Delims("${", "}").
		Funcs(map[string]any{"stringer": stringer}).
		Parse(replaceTpl)
	if err != nil {
		return "", err
	}
	bf := bytes.NewBuffer([]byte{})
	err = tpl.Execute(bf, args)
	if err != nil {
		return "", err
	}
	return bf.String(), nil
}

func builderReplaceArgs(f *ast.FuncDecl, decorName string, decorParams []string, gi *genIdentId) *ReplaceArgs {
	ra := newReplaceArgs(gi, f.Name.Name, decorName)
	// decor params
	if decorParams != nil && len(decorParams) > 0 {
		ra.HaveDecorParam = true
		ra.DecorCallParams = decorParams
	}
	// target TKind
	if f.Recv != nil && f.Recv.List != nil && len(f.Recv.List) > 0 {
		ra.TKind = "KMethod"
		ra.ReceiverVarName = f.Recv.List[0].Names[0].Name
	}
	//funcMain
	var tp *ast.FieldList
	if f.Type != nil && f.Type.TypeParams != nil {
		tp = f.Type.TypeParams
		f.Type.TypeParams = nil
	}
	closure := &ast.FuncLit{
		Type: f.Type,
		Body: f.Body,
	}
	var output []byte
	buffer := bytes.NewBuffer(output)
	err := printer.Fprint(buffer, token.NewFileSet(), closure)
	if err != nil {
		logs.Error("builderReplaceArgs printer.Fprint fail", decorName, err)
	}
	f.Type.TypeParams = tp
	ra.FuncMain = buffer.String()

	// in result
	if f.Type.Results != nil && f.Type.Results.List != nil {
		for _, r := range f.Type.Results.List {
			if r.Names != nil {
				continue
			}
			r.Names = []*ast.Ident{
				{
					NamePos: 0,
					Name:    gi.nextStr(),
					Obj:     nil,
				},
			}
		}
		count := 0
		for _, r := range f.Type.Results.List {
			if len(r.Names) == 0 {
				continue
			}
			for _, p := range r.Names {
				if p.Name == "_" {
					// fix issue #10. If the parameter name is “_”, we need to create a new name to replace it since the context will use this variable
					p.Name = gi.nextStr()
				}
				ra.OutArgNames = append(ra.OutArgNames, p.Name)
				ra.OutArgTypes = append(ra.OutArgTypes, typeString(r.Type))
				ra.DecorListOut = append(ra.DecorListOut,
					fmt.Sprintf("%s.TargetOut[%d]", ra.DecorVarName, count))
				ra.DecorCallOut = append(ra.DecorCallOut,
					//fmt.Sprintf("%s.TargetOut[%d].(%s)", ra.DecorVarName, count, typeString(r.Type)))
					fmt.Sprintf(
						"func() %s {o,_ := %s.TargetOut[%d].(%s); return o}()",
						typeString(r.Type),
						ra.DecorVarName,
						count,
						typeString(r.Type),
					),
				)
				count++
			}
		}
	}

	// in args
	if f.Type.Params.List != nil && len(f.Type.Params.List) > 0 {
		count := 0
		for _, r := range f.Type.Params.List {
			if len(r.Names) == 0 {
				continue
			}
			for _, p := range r.Names {
				if p.Name == "_" {
					// fix issue #10. If the parameter name is “_”, we need to create a new name to replace it since the context will use this variable
					p.Name = gi.nextStr()
				}
				ra.InArgNames = append(ra.InArgNames, p.Name)
				ra.InArgTypes = append(ra.InArgTypes, typeString(r.Type))
				ra.DecorCallIn = append(ra.DecorCallIn,
					//fmt.Sprintf("%s.TargetIn[%d].(%s)%s", ra.DecorVarName, count, typeString(r.Type), elString(r.Type)))
					fmt.Sprintf(
						"func() %s {o,_ := %s.TargetIn[%d].(%s); return o}()%s",
						typeString(r.Type),
						ra.DecorVarName,
						count,
						typeString(r.Type),
						elString(r.Type),
					),
				)
				count++
			}
		}
	}

	ra.HaveReturn = len(ra.OutArgNames) != 0
	return ra
}

// typeString 函数的核心功能是将 Go 语言的表达式类型（ast.Expr）转换为对应的字符串表示，并在有特殊情况（如变长参数类型）时进行适当的格式化。
//
// 示例
//
// case1 简单标识符：
//   - 输入：ast.NewIdent("myVar")
//   - 输出："myVar"
//
// case2 选择器表达式
//   - 输入：&ast.SelectorExpr{X: ast.NewIdent("pkg"), Sel: ast.NewIdent("Func")}
//   - 输出："pkg.Func"
//
// case3 变长参数
//   - 输入：表示变长参数的 AST，如 ...int
//   - 输出："[]int"
func typeString(expr ast.Expr) string {
	var output []byte
	buffer := bytes.NewBuffer(output)
	err := printer.Fprint(buffer, emptyFset, expr)
	if err != nil {
		logs.Error("typeString printer.Fprint fail", err)
	}
	s := buffer.String()
	if strings.HasPrefix(s, "...") {
		return "[]" + s[3:]
	}
	return s
}

func elString(expr ast.Expr) string {
	if strings.HasPrefix(typeString(expr), "[]") {
		return "..."
	}
	return ""
}

// stringer 是一个自定义的函数，用于将输入参数（如 InArgNames 和 OutArgNames）转换为字符串表示，通常是以逗号分隔的列表。
func stringer(elems []string) string {
	if elems == nil {
		return ""
	}
	return strings.Join(elems, ", ")
}

func randStr(le int) string {
	s := ""
	for i := 0; i < le; i++ {
		index := rand.Intn(len(randSeeds))
		s += string(randSeeds[index])
	}
	return s
}

type genIdentId struct {
	id    int
	ident string
}

func newGenIdentId() *genIdentId {
	suf := randStr(6)
	return &genIdentId{
		id:    0,
		ident: "_decorGenIdent" + suf,
	}
}

func (g *genIdentId) next() int {
	g.id++
	return g.id
}

func (g *genIdentId) nextStr() string {
	g.next()
	return g.ident + strconv.Itoa(g.id)
}

// TODO
func funIsDecorator(fd *ast.FuncDecl, pkgName string) bool {
	if pkgName == "" ||
		fd == nil ||
		fd.Recv != nil ||
		fd.Type == nil ||
		fd.Type.Params == nil ||
		fd.Type.Params.NumFields() != 1 ||
		fd.Type.Params.List[0] == nil ||
		fd.Type.Params.List[0].Type == nil {
		return false
	}
	expr := fd.Type.Params.List[0].Type
	buffer := bytes.NewBuffer([]byte{})
	err := printer.Fprint(buffer, emptyFset, expr)
	if err != nil {
		logs.Debug("funIsDecorator printer.Fprint fail", err)
		return false
	}
	return strings.TrimSpace(buffer.String()) == fmt.Sprintf("*%s.Context", pkgName)
}

func getStmtList(s string) (r []ast.Stmt, i int, err error) {
	s = "func(){\n" + s + "\n}()"
	//logs.Debug("getStmtList", s)
	expr, err := parser.ParseExpr(s)
	if err != nil {
		return
	}
	r = expr.(*ast.CallExpr).Fun.(*ast.FuncLit).Body.List
	i = 0
	return
}

// same like /usr/local/go/src/go/parser/interface.go:139#ParseDir
func parserGOFiles(fset *token.FileSet, files ...string) (*ast.Package, error) {
	var pkg *ast.Package
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			return pkg, err
		}
		if pkg == nil {
			pkg = &ast.Package{
				Name:  f.Name.Name,
				Files: make(map[string]*ast.File),
			}
		}
		pkg.Files[file] = f
	}
	return pkg, nil
}

func assignStmtPos(f, t ast.Node, depth bool) {
	if f == nil || t == nil {
		return
	}
	switch v := f.(type) {
	case nil:
		return
	case *ast.Ident:
		v.NamePos = t.Pos()
	case *ast.BasicLit:
		if v != nil {
			v.ValuePos = t.Pos()
		}
	case *ast.UnaryExpr:
		v.OpPos = t.Pos()
		if depth {
			assignStmtPos(v.X, t, depth)
		}
	case *ast.IndexExpr:
		v.Lbrack = t.Pos()
		v.Rbrack = t.Pos()
		assignStmtPos(v.X, t, depth)
		assignStmtPos(v.Index, t, depth)
	case *ast.AssignStmt:
		v.TokPos = t.Pos()
		if depth {
			for _, lhs := range v.Lhs {
				assignStmtPos(lhs, t, depth)
			}
			for _, rhs := range v.Rhs {
				assignStmtPos(rhs, t, depth)
			}
		}
	case *ast.CompositeLit:
		v.Lbrace = t.Pos()
		v.Rbrace = t.End()
		if depth {
			assignStmtPos(v.Type, t, depth)
			if v.Elts != nil {
				for _, els := range v.Elts {
					assignStmtPos(els, t, depth)
				}
			}
		}
	case *ast.KeyValueExpr:
		v.Colon = t.Pos()
		if depth {
			assignStmtPos(v.Key, t, depth)
			assignStmtPos(v.Value, t, depth)
		}
	case *ast.ArrayType:
		v.Lbrack = t.Pos()
		if depth {
			assignStmtPos(v.Len, t, depth)
			assignStmtPos(v.Elt, t, depth)
		}
	case *ast.SelectorExpr:
		assignStmtPos(v.Sel, t, depth)
		assignStmtPos(v.X, t, depth)
	case *ast.FuncLit:
		assignStmtPos(v.Type, t, depth)
		if depth {
			assignStmtPos(v.Body, t, depth)
		}
	case *ast.FuncType:
		v.Func = t.Pos()
		assignStmtPos(v.Params, t, depth)
		assignStmtPos(v.Results, t, depth)
	case *ast.BlockStmt:
		v.Lbrace = t.Pos()
		v.Rbrace = t.End()
		if depth && v.List != nil {
			for _, st := range v.List {
				assignStmtPos(st, t, depth)
			}
		}
	case *ast.TypeAssertExpr:
		v.Lparen = t.Pos()
		v.Rparen = t.End()
		assignStmtPos(v.Type, t, depth)
		assignStmtPos(v.X, t, depth)
	case *ast.FieldList:
		if v == nil {
			return
		}
		v.Opening = t.Pos()
		v.Closing = t.Pos()
		if depth && v.List != nil {
			for _, field := range v.List {
				assignStmtPos(field, t, depth)
			}
		}
	case *ast.Field:
		if v == nil {
			return
		}
		assignStmtPos(v.Type, t, depth)
		assignStmtPos(v.Tag, t, depth)
		if v.Names != nil {
			for _, name := range v.Names {
				assignStmtPos(name, t, depth)
			}
		}
	case *ast.CallExpr:
		v.Lparen = t.Pos()
		v.Rparen = t.Pos()
		if v.Args != nil {
			for _, arg := range v.Args {
				assignStmtPos(arg, t, depth)
			}
		}
		if depth {
			assignStmtPos(v.Fun, t, depth)
		}
	default:
		logs.Info("can`t support type from assignStmtPos")
	}
}

// 示例
//
//	假设我们有一个简单的函数：
//		func Add(a, b int) int {
//		   return a + b
//		}
//
//	我们为这个函数生成装饰器的代码，传递的 ReplaceArgs 可能是：
//		ReplaceArgs{
//		   TKind:           "KFunc",
//		   TargetName:      `"Add"`,				 // 目标函数
//		   ReceiverVarName: `"nil"`,
//		   DecorVarName:    `"AddDecor"`,            // 装饰器变量名
//		   DecorCallName:   `"AddDecorCall"`,		 // 装饰器函数名
//		   FuncMain:        `"Add(a, b)"`,			 // 被装饰函数
//		   InArgNames:      `[]string{"a", "b"}`,	 // 被装饰函数参数列表
//		   OutArgNames:     `[]string{"result"}`,	 // 被装饰函数返回值列表
//		   HaveReturn:      true,					 // 是否有返回值
//		}
//
//	在这种情况下，模板将生成类似以下的代码：
//		AddDecor := &decor.Context{
//		   Kind:       decor.KFunc,
//		   TargetName: "Add",
//		   Receiver:   "nil",
//		   TargetIn:   []any{"a", "b"},
//		   TargetOut:  []any{"result"},
//		}
//		AddDecor.Func = func() {
//		   result = Add(a, b)
//		}
//		AddDecorCall(AddDecor)
//		return result
//
