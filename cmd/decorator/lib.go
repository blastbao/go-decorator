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

// ast.FuncDecl 结构体：
//
//	 FuncDecl struct {
//			Doc  *CommentGroup // associated documentation; or nil
//			Recv *FieldList    // receiver (methods); or nil (functions)
//			Name *Ident        // function/method name
//			Type *FuncType     // function signature: type and value parameters, results, and position of "func" keyword
//			Body *BlockStmt    // function body; or nil for external (non-Go) function
//	}
//
// 示例函数：
//
//	// This is a sample function
//	func example(r *ReceiverType, a int) int {
//	   	return a
//	}
//
// 说明:
//
//   - Doc: 注释 // This is a sample function
//   - Recv: 接收者 (r *ReceiverType)
//   - Name: 函数或方法名 example
//   - Type: 函数签名，包括参数和返回值 func(a int) int
//   - Body: 函数体 { return a }
//
// ast.FuncType 结构体：
//
//	FuncType struct {
//		Func       token.Pos  // position of "func" keyword (token.NoPos if there is no "func")
//		TypeParams *FieldList // type parameters; or nil
//		Params     *FieldList // (incoming) parameters; non-nil
//		Results    *FieldList // (outgoing) results; or nil
//	}
//
// 示例函数：
//
//	func example[T any](a T, b string) (int, error) {
//	   return 0, nil
//	}
//
// 说明:
//   - Func: "func" 关键字的位置。
//   - TypeParams: 包含类型参数 [T any]。
//   - Params: 包含输入参数 (a T, b string)。
//   - Results: 包含返回值 (int, error)。
func builderReplaceArgs(f *ast.FuncDecl, decorName string, decorParams []string, gi *genIdentId) *ReplaceArgs {
	ra := newReplaceArgs(gi, f.Name.Name, decorName)

	// 如果装饰器有参数，填充相关字段
	if decorParams != nil && len(decorParams) > 0 {
		ra.HaveDecorParam = true
		ra.DecorCallParams = decorParams
	}

	// 判断是否有接收者（方法的接收者），并设置其类型
	if f.Recv != nil && f.Recv.List != nil && len(f.Recv.List) > 0 {
		ra.TKind = "KMethod"
		ra.ReceiverVarName = f.Recv.List[0].Names[0].Name
	}

	// 假设我们有以下泛型函数：
	//
	//	func Add[T int | float64](a T, b T) T {
	//    	return a + b
	//	}
	//
	// 将类型(泛型)参数移除后，得到闭包：
	//
	//  func(a T, b T) T {
	// 		return a + b
	//	}
	//
	// Q: 为啥要临时移除泛型类型参数 T ？
	// A:
	// Go 的 ast 包和打印机制是根据具体的 AST 节点结构进行操作的，泛型类型参数（如 T）会使 AST 的结构更加复杂。
	// 如果你直接传递一个包含类型参数的泛型函数到 printer.Fprint，生成的 Go 代码可能过于复杂，或者在打印过程中出现错误。
	//
	// 例如，泛型函数的类型参数（TypeParams）通常是一个复杂的类型，特别是在泛型函数使用了联合类型约束（如 int | float64）时。
	// 这些信息在函数体之外可能并不直接需要。如果直接打印这些复杂信息，可能会引发不必要的困难，尤其是在构造闭包或者处理一些不需要类型参数的情况时。
	//
	// 在构建闭包时，泛型类型参数并不直接影响闭包的内部逻辑，通常我们只关心 闭包的函数体，而不是类型参数。
	// 为了简化这个过程，通过暂时移除泛型类型参数，使得 printer.Fprint 打印出的只是函数体部分，而不包含冗余的泛型信息。

	var tp *ast.FieldList
	if f.Type != nil && f.Type.TypeParams != nil {
		tp = f.Type.TypeParams  // 将函数的类型参数保存到变量 tp 中，之后会用来恢复类型参数。
		f.Type.TypeParams = nil // 将函数的类型参数设置为 nil ，这通常是为了在打印或处理函数时避免类型参数的干扰。
	}

	// 创建了一个匿名函数（闭包），这个闭包继承了函数 f 的类型和函数体。ast.FuncLit 表示 函数字面量（匿名函数）。
	closure := &ast.FuncLit{
		Type: f.Type, // 使用函数 f 的类型，这个类型已经去掉了类型参数
		Body: f.Body, // 使用函数 f 的函数体
	}

	var output []byte
	buffer := bytes.NewBuffer(output)
	err := printer.Fprint(buffer, token.NewFileSet(), closure)
	if err != nil {
		logs.Error("builderReplaceArgs printer.Fprint fail", decorName, err)
	}
	f.Type.TypeParams = tp
	ra.FuncMain = buffer.String() // 保存闭包的字符串表示

	// 处理函数返回值，收集其名称和类型
	//
	//
	// 假设我们有以下函数，并且没有为返回值提供名称：
	//
	//	func Calculate(a, b int) (int, int) {
	//    	return a + b, a - b
	//	}
	//
	// 假设我们有以下函数：
	//
	//	func compute(x int) (int, error) {
	//	   return x * 2, nil
	//	}
	//
	// 1. f.Type.Results.List 包含两个返回值 (int, error)，因为没有名称，所以为其生成名称如 res1 和 err.
	// 2. 记录返回值的名称和类型：
	//	ra.OutArgNames = ["res1", "err"]
	//	ra.OutArgTypes = ["int", "error"]
	// 3. 生成装饰器调用：
	//	ra.DecorListOut = [ "DecorVarName.TargetOut[0]", "DecorVarName.TargetOut[1]" ]
	//	ra.DecorCallOut = [ "func() int { o, _ := DecorVarName.TargetOut[0].(int); return o }()", "func() error { o, _ := DecorVarName.TargetOut[1].(error); return o }()" ]

	// 检查该函数是否有返回值
	if f.Type.Results != nil && f.Type.Results.List != nil {
		// 遍历返回值
		for _, r := range f.Type.Results.List {
			// 返回值已经有名称，不需要生成新的名称，跳过
			if r.Names != nil {
				continue
			}
			// 返回值没有名称，为其生成一个新的名字
			r.Names = []*ast.Ident{
				{
					NamePos: 0,
					Name:    gi.nextStr(), // 生成新的名称（一个递增的标识符）
					Obj:     nil,
				},
			}
		}

		// 返回值序号
		count := 0
		// 遍历返回值
		for _, r := range f.Type.Results.List {
			if len(r.Names) == 0 {
				continue
			}
			// 遍历当前返回值的名称（每个返回值可能有多个名称）
			for _, p := range r.Names {
				// 如果返回值的名称为 "_" ，为它生成一个新的名字。
				if p.Name == "_" {
					// fix issue #10. If the parameter name is “_”, we need to create a new name to replace it since the context will use this variable
					p.Name = gi.nextStr()
				}
				// 将返回值名称添加到 ra.OutArgNames 中。
				ra.OutArgNames = append(ra.OutArgNames, p.Name)
				// 将返回值类型添加到 ra.OutArgTypes 中。typeString 是一个方法，用于将返回值的类型转换为字符串形式。
				ra.OutArgTypes = append(ra.OutArgTypes, typeString(r.Type))
				ra.DecorListOut = append(ra.DecorListOut, fmt.Sprintf("%s.TargetOut[%d]", ra.DecorVarName, count))
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

				// 处理下一个返回值。
				count++
			}
		}
	}

	// 检查函数的参数列表是否存在且非空
	if f.Type.Params.List != nil && len(f.Type.Params.List) > 0 {
		// 给每个参数分配一个唯一的索引值
		count := 0
		// 遍历参数列表
		for _, r := range f.Type.Params.List {
			// 如果参数没有名称（例如 _），就跳过
			if len(r.Names) == 0 {
				continue
			}
			// 遍历每个参数的名称
			for _, p := range r.Names {
				// 生成的新名称
				if p.Name == "_" {
					// fix issue #10. If the parameter name is “_”, we need to create a new name to replace it since the context will use this variable
					p.Name = gi.nextStr()
				}
				// 存储所有输入参数的名称。
				ra.InArgNames = append(ra.InArgNames, p.Name)
				// 存储所有输入参数的类型。
				ra.InArgTypes = append(ra.InArgTypes, typeString(r.Type))

				// 闭包函数：func() int { o,_ := decorator.TargetIn[0].(int); return o }()
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
