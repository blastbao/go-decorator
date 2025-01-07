package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/printer"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dengsgo/go-decorator/cmd/logs"
)

const msgDecorPkgNotImported = "decorator used but package not imported (need add `import _ \"" + decoratorPackagePath + "\"`)"
const msgDecorPkgNotFound = "decor package is not found"
const msgCantUsedOnDecoratorFunc = `decorators cannot be used on decorators`

var packageInfo *_packageInfo

var printerCfg = &printer.Config{Tabwidth: 8, Mode: printer.SourcePos}

func compile(args []string) error {
	{
		var err error
		// go list -json -find 会返回当前模块下的包信息
		packageInfo, err = getPackageInfo("")
		if err != nil || packageInfo.Module.Path == "" {
			logs.Error("doesn't seem to be a Go project:", err)
		}
	}

	projectName := packageInfo.Module.Path
	logs.Debug("projectName", projectName)
	//log.Printf("TOOLEXEC_IMPORTPATH %+v\n", os.Getenv("TOOLEXEC_IMPORTPATH"))

	files := make([]string, 0, len(args))
	packageName := ""
	for i, arg := range args {
		// 如果当前参数是 -p 且后面还有参数（即 i+1 < len(args)），则说明后面跟的是包名。
		if arg == "-p" && i+1 < len(args) {
			packageName = args[i+1]
		}
		// 如果当前参数是以 - 开头的标志参数（例如 -p），则跳过。
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// 如果参数是以 $projectDir/ 开头，并且是以 .go 后缀结尾，表示这是一个 Go 源文件的路径。
		// 将从当前位置开始的所有参数都视为 Go 文件路径，并赋值给 files 。
		// 找到符合条件的 Go 源文件后，跳出循环。
		if strings.HasPrefix(arg, projectDir+string(filepath.Separator)) && strings.HasSuffix(arg, ".go") {
			files = args[i:]
			break
		}
	}

	// 如果包名不是 main 且不是以项目名作为前缀（例如，包名不属于当前 Go 项目），则认为包名不符合要求，直接返回；
	// 如果没有找到符合条件的 Go 文件路径（即 files 为空），直接返回；
	if (packageName != "main" && !strings.HasPrefix(packageName, projectName)) || len(files) == 0 {
		return nil
	}

	// 如果能够成功获取到 decoratorPackagePath 包的信息，则生成一个 wrapped_code.go 文件的路径，并将其添加到 files 列表中，供后续处理。
	decorWrappedCodeFilePath := ""
	if dpp, err := getPackageInfo(decoratorPackagePath); err == nil {
		decorWrappedCodeFilePath = dpp.Dir + "/wrapped_code.go"
		files = append(files, decorWrappedCodeFilePath)
	}

	logs.Debug("packageName", packageName, files, args)

	// 把每个源文件解析为 ast
	fset := token.NewFileSet()
	pkg, err := parserGOFiles(fset, files...)
	if err != nil {
		logs.Error(err)
	}

	errPos, err := typeDecorRebuild(pkg)
	if err != nil {
		logs.Error(err, biSymbol, friendlyIDEPosition(fset, errPos))
	}

	// 存储当前处理文件的路径
	var originPath string
	for file, f := range pkg.Files {
		logs.Debug("file Parse", file)
		if file == decorWrappedCodeFilePath {
			continue // ignore
		}
		//f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		//if err != nil {
		//	continue
		//}
		logs.Debug(f.Decls)

		// imp 中存储了 file 的所有导入项
		imp := newImporter(f)

		// 标记文件是否被更新
		updated := false

		// 遍历文件 file 中每个函数声明
		visitAstDecl(f, func(fd *ast.FuncDecl) (r bool) {
			// 无注释则忽略
			if fd.Doc == nil || fd.Doc.List == nil || len(fd.Doc.List) == 0 {
				return
			}
			//log.Printf("%+v\n", fd)

			originPath = file
			var collDecors []*decorAnnotation
			mapDecors := newMapV[string, *ast.Comment]()

			// 有注释则遍历
			for i := len(fd.Doc.List) - 1; i >= 0; i-- {
				doc := fd.Doc.List[i]
				// 是否以 "//go:decor " 开头
				//
				// 例如：
				// //go:decor logging
				// //go:decor appendFile
				// //go:decor fun1.DecorHandlerFunc
				// //go:decor levelLogging#{level: "debug"}
				// //go:decor hit#{msg: "message from decor", repeat: true, count: 10, f:1}
				// func datetime(timestamp int64) string {
				//     return time.Unix(timestamp, 0).String()
				// }
				if !strings.HasPrefix(doc.Text, decoratorScanFlag) {
					break
				}
				logs.Debug("HIT:", doc.Text)
				// 从 //go:decor 注释解析出 decorFuncName, decorFuncArgs
				decorName, decorArgs, err := parseDecorAndParameters(doc.Text[len(decoratorScanFlag):])
				logs.Debug(decorName, decorArgs, err)
				if err != nil {
					logs.Error(err, biSymbol, friendlyIDEPosition(fset, doc.Pos()))
				}
				// 不许重复修饰
				if !mapDecors.put(decorName, doc) {
					logs.Error("cannot use the same decorator for repeated decoration", biSymbol,
						"Decor:", friendlyIDEPosition(fset, doc.Pos()), biSymbol,
						"Repeated:", friendlyIDEPosition(fset, mapDecors.get(decorName).Pos()))
				}
				// 保存 decorate 相关注释
				collDecors = append(collDecors, newDecorAnnotation(doc, decorName, decorArgs))
			}

			// 当前函数无需修饰
			if len(collDecors) == 0 {
				return
			}

			logs.Info("find the entry for using the decorator", friendlyIDEPosition(fset, fd.Pos()))
			logs.Debug("collDecors", collDecors)

			// 生成一个随机标识符
			gi := newGenIdentId()

			// 链式修饰
			for _, da := range collDecors {
				logs.Debug("handler:", da.doc.Text)
				// 检查 decorName 是不是装饰器
				//if fd.Recv != nil {
				//	logs.Error("decorators cannot decorate struct method", biSymbol,
				//		friendlyIDEPosition(fset, fd.Recv.Pos()))
				//	continue
				//}
				decorName, decorParams := da.name, da.parameters
				logs.Debug(decorName, decorParams)

				// check self is not decorator function
				// 检查装饰器包是否已导入：判断 f 是否已导入 "github.com/dengsgo/go-decorator/decor"
				pkgDecorName, ok := imp.importedPath(decoratorPackagePath)
				if !ok {
					// 未导入报错
					logs.Error(msgDecorPkgNotImported, biSymbol,
						"Target:", friendlyIDEPosition(fset, fd.Pos()), biSymbol,
						"Decor:", friendlyIDEPosition(fset, da.doc.Pos()))
				} else if pkgDecorName == "_" {
					// 若为 "_" 类型导入，强制修改别名为 decor
					imp.pathObjMap[decoratorPackagePath].Name = nil // rewrite this package import way
					imp.pathMap[decoratorPackagePath] = "decor"     // mark finished
					pkgDecorName = "decor"
				}

				// 如果当前函数已经是 decoratorFunc ，则不许对其 decorate
				if funIsDecorator(fd, pkgDecorName) {
					logs.Error(msgCantUsedOnDecoratorFunc, biSymbol, friendlyIDEPosition(fset, fd.Pos()))
				}

				// got package path
				// 存储装饰器所在包的路径
				decorPkgPath := ""
				// 获取装饰器的包名 x
				if x := decorX(decorName); x != "" {
					// 检查当前文件是否已经导入包 x ，如果导入了，获取包的路径 xPath 。
					if xPath, ok := imp.importedName(x); ok {
						// 获取 x 包的别名
						name, _ := imp.importedPath(xPath)
						// 如果 x 包的别名为 "_" ，表示包被匿名导入，需要重置其别名以便使用
						if name == "_" {
							imp.pathObjMap[xPath].Name = nil // 将 imp.pathObjMap[xPath].Name 设为 nil，这会重写包的导入方式。
							imp.pathMap[xPath] = x           // 设置别名。
						}
						decorPkgPath = xPath
					} else {
						// 如果包 x 未导入，记录错误日志，指出包未找到，并提供注释位置
						logs.Error(x, "package not found", biSymbol, friendlyIDEPosition(fset, da.doc.Pos()))
					}
				}

				// 获取指定路径 decorPkgPath 下函数 decorName 的参数信息
				params, err := checkDecorAndGetParam(decorPkgPath, decorName, decorParams)
				if err != nil {
					logs.Error(err, biSymbol, "Decor:", friendlyIDEPosition(fset, da.doc.Pos()))
				}

				ra := builderReplaceArgs(fd, decorName, params, gi)
				rs, err := replace(ra)
				if err != nil {
					logs.Error(err)
				}

				//	模板 replaceTpl 生成类似的代码：
				//
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
				genStmts, _, err := getStmtList(rs)
				if err != nil {
					logs.Error("getStmtList err", err)
				}

				if wcf, ok := pkg.Files[decorWrappedCodeFilePath]; ok {
					assignWrappedCodePos(genStmts, wcf.Decls[0].(*ast.FuncDecl).Body.List, wcf.Comments)
				}

				// 根据是否有返回值，替换生成的函数体
				// genStmts[1] 对应 "AddDecor.Func = func()..."
				if len(ra.OutArgNames) == 0 {
					// non-return
					genStmts[1].(*ast.AssignStmt).Rhs[0].(*ast.FuncLit).Body.List[0].(*ast.ExprStmt).X.(*ast.CallExpr).Fun.(*ast.FuncLit).Body.List = fd.Body.List
				} else {
					// has return
					genStmts[1].(*ast.AssignStmt).Rhs[0].(*ast.FuncLit).Body.List[0].(*ast.AssignStmt).Rhs[0].(*ast.CallExpr).Fun.(*ast.FuncLit).Body.List = fd.Body.List
				}

				// genStmts[2] 对应 "AddDecorCall(AddDecor)"
				ce := genStmts[2].(*ast.ExprStmt).X.(*ast.CallExpr)
				assignCorrectPos(da.doc, ce)

				fd.Body.List = genStmts
				//x.Body.Rbrace = x.Body.Lbrace + token.Pos(ofs)
				//log.Printf("fd.Body.Pos() %+v\n", fd.Body.Pos())
				updated = true
			}
			return
		},
		)

		// 未发生更新，忽略
		if !updated {
			continue
		}

		/// 将修改后的代码写入临时文件，并更新构建参数，使得后续的构建过程使用新的代码文件。

		// 将 AST f 打印到缓冲区
		var output []byte
		buffer := bytes.NewBuffer(output)
		err = printerCfg.Fprint(buffer, fset, f)
		if err != nil {
			return errors.New("fprint original code")
		}

		// 写入临时文件
		tgDir := path.Join(tempDir, os.Getenv("TOOLEXEC_IMPORTPATH"))
		_ = os.MkdirAll(tgDir, 0777)
		tmpEntryFile := path.Join(tgDir, filepath.Base(originPath))
		logs.Debug("originPath", originPath, filepath.Base(originPath))
		err = os.WriteFile(tmpEntryFile, buffer.Bytes(), 0777)
		if err != nil {
			logs.Error("fail write into temporary file", err.Error())
		}

		// 将原始文件路径替换为临时文件路径
		for i := range args {
			if args[i] == originPath {
				args[i] = tmpEntryFile
			}
		}

		// 记录调试信息
		logs.Debug("args updated", args)
		logs.Debug("rewrite file", originPath, "=>", tmpEntryFile)
	}

	return nil
}

func decorX(decorName string) string {
	arr := strings.Split(decorName, ".")
	if len(arr) != 2 {
		return ""
	}
	return arr[0]
}

func visitAstDecl(f *ast.File, funVisitor func(*ast.FuncDecl) bool) {
	if f.Decls == nil || funVisitor == nil {
		return
	}
LOOP:
	// 遍历每个声明
	for _, t := range f.Decls {
		if t == nil {
			continue
		}
		switch decl := t.(type) {
		case *ast.FuncDecl:
			// 对于函数声明，执行 visitor ，如果返回 true 则停止遍历
			if funVisitor(decl) {
				break LOOP
			}
		}
	}
}

func assignWrappedCodePos(from, reset []ast.Stmt, cg []*ast.CommentGroup) {
	{
		partFrom := from[0].(*ast.AssignStmt)
		partReset := reset[0].(*ast.AssignStmt)
		partFrom.TokPos = partReset.Pos()
		partFrom.Tok = partReset.Tok
		assignStmtPos(partFrom.Lhs[0], partReset.Lhs[0], true)
		assignStmtPos(partFrom.Rhs[0], partReset.Rhs[0], false)
		{
			l := partFrom.Rhs[0].(*ast.UnaryExpr).X.(*ast.CompositeLit)
			r := partReset.Rhs[0].(*ast.CompositeLit)
			l.Lbrace = r.Lbrace
			l.Rbrace = r.Rbrace
			assignStmtPos(l.Type, r.Type, true)
			//l.Type.(*ast.SelectorExpr).X.(*ast.Ident).NamePos = r.Type.(*ast.Ident).NamePos
			for i, kv := range l.Elts {
				rv := r.Elts[i].(*ast.KeyValueExpr)
				v := kv.(*ast.KeyValueExpr)
				assignStmtPos(v, rv, true)
			}
		}
	}
	{
		partFrom := from[1].(*ast.AssignStmt)
		partReset := reset[1].(*ast.AssignStmt)
		assignStmtPos(partFrom.Lhs[0], partReset.Lhs[0], true)
		//partFrom.Lhs[0].(*ast.SelectorExpr).X.(*ast.Ident).NamePos = partReset.Lhs[0].(*ast.SelectorExpr).X.(*ast.Ident).NamePos
		//partFrom.Lhs[0].(*ast.SelectorExpr).Sel.NamePos = partReset.Lhs[0].(*ast.SelectorExpr).Sel.NamePos
		partFrom.Tok = partReset.Tok
		//partFrom.Rhs[0].(*ast.FuncLit)
		assignStmtPos(partFrom.Rhs[0], partReset.Rhs[0], true)
		var flit *ast.CallExpr
		r := partReset.Rhs[0].(*ast.FuncLit).Body.List[0].(*ast.ExprStmt).X.(*ast.CallExpr)
		if astmt, ok := partFrom.Rhs[0].(*ast.FuncLit).Body.List[0].(*ast.AssignStmt); ok {
			assignStmtPos(astmt.Lhs[0], r, true)
			flit = partFrom.Rhs[0].(*ast.FuncLit).Body.List[0].(*ast.AssignStmt).Rhs[0].(*ast.CallExpr)
		} else {
			flit = partFrom.Rhs[0].(*ast.FuncLit).Body.List[0].(*ast.ExprStmt).X.(*ast.CallExpr)
		}
		//flit.Lparen = r.Lparen
		//TODO
		if flit.Args != nil {
			inParams := getIndexComment(cg, 12)
			for _, arg := range flit.Args {
				assignStmtPos(arg, inParams, true)
			}
		}
	}
	// has-return
	if len(from) > 3 {
		l := from[3].(*ast.ReturnStmt)
		r := reset[2].(*ast.ReturnStmt)
		l.Return = r.Return
		outParams := getIndexComment(cg, 14)
		if l.Results != nil && outParams != nil {
			for _, v := range l.Results {
				assignStmtPos(v, outParams, true)
			}
		}
	}
}

func getIndexComment(cg []*ast.CommentGroup, index int) *ast.Comment {
	if len(cg) > index && cg[index] != nil && cg[index].List != nil && len(cg[index].List) > 0 {
		return cg[index].List[0]
	}
	return nil
}

// Reset the line of the behavior annotation where the decorator call is located
func assignCorrectPos(doc *ast.Comment, ce *ast.CallExpr) {
	ce.Lparen = doc.Pos()
	ce.Rparen = doc.Pos()
	offset := token.Pos(0)
	if t, ok := ce.Fun.(*ast.Ident); ok {
		t.NamePos = doc.Pos()
		offset = token.Pos(len(t.Name))
	} else if t, ok := ce.Fun.(*ast.SelectorExpr); ok {
		if id, ok := t.X.(*ast.Ident); ok {
			id.NamePos = doc.Pos()
			offset = token.Pos(len(id.Name))
		}
		//t.Sel.NamePos = doc.Pos() + offset + 1
		t.Sel.NamePos = doc.Pos()
		offset += token.Pos(len(t.Sel.Name)) + 1
	}
	for _, arg := range ce.Args {
		//ast.Print(token.NewFileSet(), arg)
		//if id, ok := arg.(*ast.Ident); ok {
		//	//id.NamePos = doc.Pos() + offset
		//	id.NamePos = doc.Pos()
		//}
		switch arg := arg.(type) {
		case *ast.Ident:
			arg.NamePos = doc.Pos()
		case *ast.BasicLit:
			arg.ValuePos = doc.Pos()
		case *ast.UnaryExpr:
			arg.OpPos = doc.Pos()
			if a, ok := arg.X.(*ast.Ident); ok {
				a.NamePos = doc.Pos()
			}
		}
	}
}

func reverseSlice[T any](ele []T) []T {
	r := make([]T, len(ele))
	for i, v := range ele {
		r[len(ele)-1-i] = v
	}
	return r
}

// 它遍历 AST 中的 ast.GenDecl 类型的声明，寻找类型声明（ast.TypeSpec），并将每个找到的类型声明和相关文档注释（ast.CommentGroup）传递给回调函数。
func typeDeclVisitor(decls []ast.Decl, fn func(*ast.TypeSpec, *ast.CommentGroup)) {
	if decls == nil || len(decls) == 0 {
		return
	}
	for _, decl := range decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Specs == nil || len(gd.Specs) == 0 {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			fn(ts, gd.Doc)
		}
	}
}

func typeDecorRebuild(pkg *ast.Package) (pos token.Pos, err error) {
	// 从注释组中提取以特定前缀（decoratorScanFlag）开头的装饰器注释。
	findAndCollDecorComments := func(cg *ast.CommentGroup) []*ast.Comment {
		// 从后向前收集以 "//go:decor " 开头的注释
		comments := make([]*ast.Comment, 0)
		if cg == nil || cg.List == nil {
			return comments
		}
		for i := len(cg.List) - 1; i >= 0; i-- {
			if !strings.HasPrefix(cg.List[i].Text, decoratorScanFlag) {
				break
			}
			comments = append(comments, cg.List[i])
		}
		// 将顺序反转
		return reverseSlice(comments)
	}

	// 存储每个类型对应的装饰器注释。键是类型名，值是注释列表。
	typeNameMapDecorComments := map[string][]*ast.Comment{}

	// 存储错误信息，包括位置和错误详情
	type errSet struct {
		pos token.Pos
		err error
	}
	var errs []*errSet

	// 遍历包中的每个文件
	for _, f := range pkg.Files {
		// 遍历每个文件中的每个类型声明
		typeDeclVisitor(f.Decls, func(spec *ast.TypeSpec, typeDoc *ast.CommentGroup) {
			// 如果类型声明 (spec.Doc) 和类型注释 (typeDoc) 都不存在或为空，则返回。
			//
			// spec.Doc 是类型声明的文档注释。
			// typeDoc 是类型本身的注释。
			//
			//  // Foo is a simple struct
			//	/* Decorator:example */
			//	type Foo struct {
			//	   Name string
			//	}
			//
			//  对于结构体 Foo ：
			//	- spec 是一个 ast.TypeSpec，表示类型声明。
			//	- spec.Name 是一个 ast.Ident，表示标识符。
			//	- spec.Name.Name 是字符串 "Foo"，表示类型的名称。
			//	- spec.Doc 包含 // Foo is a simple struct 。
			//	- typeDoc 包含 /* Decorator:example */ 。
			//
			if (spec.Doc == nil || spec.Doc.List == nil) && (typeDoc == nil || typeDoc.List == nil) {
				return
			}

			// 从注释中提取装饰器注释。
			comments := findAndCollDecorComments(spec.Doc)
			//log.Printf("findAndCollDecorComments(spec.Doc): %+v \n", comments)
			comments = append(comments, findAndCollDecorComments(typeDoc)...)
			//log.Printf("append(comments, findAndCollDecorComments(typeDoc)...): %+v \n", comments)
			if len(comments) == 0 {
				return
			}

			// 如果类型名称重复声明，记录错误
			if _, ok := typeNameMapDecorComments[spec.Name.Name]; ok {
				errs = append(errs, &errSet{
					pos: spec.Name.NamePos,
					err: errors.New("duplicate type definition: " + spec.Name.Name),
				})
				return
			}
			// 保存类型名称的注释
			typeNameMapDecorComments[spec.Name.Name] = comments
		})
		if len(errs) > 0 {
			return errs[0].pos, errs[0].err
		}
	}

	//log.Printf("typeNameMapDecorComments: %+v \n", typeNameMapDecorComments)
	//log.Printf("errs: %+v \n", errs)
	if len(typeNameMapDecorComments) == 0 {
		return
	}

	// 获取表达式的标识符名称，支持普通变量、泛型、指针等多种形式。
	identName := func(expr ast.Expr) string {
		switch expr := expr.(type) {
		case *ast.Ident: // normal: var
			// 普通变量标识符，直接返回标识符的名称。
			// 示例：对于表达式 x，返回 "x"。
			return expr.Name
		case *ast.IndexListExpr: // var[T]
			// 处理形如 var[T] 的表达式，如果 expr.X 是标识符，则返回其名称。
			// 示例：对于表达式 List[int]，返回 "List"。
			if v, ok := expr.X.(*ast.Ident); ok {
				return v.Name
			}
			return ""
		case *ast.IndexExpr: //  var[K,V]
			// 处理形如 var[K,V] 的表达式，如果 expr.X 是标识符，则返回其名称。
			// 示例：对于表达式 Map[string, int]，返回 "Map"。
			if v, ok := expr.X.(*ast.Ident); ok {
				return v.Name
			}
			return ""
		case *ast.StarExpr: // pointer
			// 处理指针类型，进一步检查指针所指的类型。
			// 包含三种情况：
			//	普通指针：*var，返回 "var"。
			//	泛型指针：*var[K]，返回 "var"。
			//	多参数泛型指针：*var[K,V]，返回 "var"。
			// 示例：对于表达式 *Node，返回 "Node"。
			switch x := expr.X.(type) {
			case *ast.Ident: // *var
				return x.Name
			case *ast.IndexExpr: // *var[K]
				if v, ok := x.X.(*ast.Ident); ok {
					return v.Name
				}
				return ""
			case *ast.IndexListExpr: // *var[K,V]
				if v, ok := x.X.(*ast.Ident); ok {
					return v.Name
				}
				return ""
			default:
				return ""
			}
		}
		return ""
	}

	// 遍历包中的每个文件
	for _, f := range pkg.Files {
		// 遍历文件中的每个声明，寻找函数声明 (ast.FuncDecl)
		visitAstDecl(f, func(decl *ast.FuncDecl) (r bool) {
			// 确保函数是一个方法（即，必须有一个接收者），检查接收者列表是否存在且仅有一个接收者。
			if decl.Recv == nil || decl.Recv.List == nil || len(decl.Recv.List) != 1 || decl.Recv.List[0].Type == nil {
				return
			}
			// 获取接收者类型的名称。
			typeIdName := identName(decl.Recv.List[0].Type)
			if typeIdName == "" {
				return
			}
			// 查找该类型的装饰器注释，如果找不到或注释列表为空，则返回
			comments, ok := typeNameMapDecorComments[typeIdName]
			if !ok || len(comments) == 0 {
				return
			}
			//log.Printf("decl: %+v, comments: %+v\n", decl, comments)

			// 如果函数声明没有文档注释 (decl.Doc)，则创建一个新的注释组，并将装饰器注释列表赋给它。
			if decl.Doc == nil {
				decl.Doc = &ast.CommentGroup{List: comments}
			} else {
				// 如果已经有文档注释，则将装饰器注释附加到现有的注释列表中。
				decl.Doc.List = append(decl.Doc.List, comments...)
			}
			return
		})
	}

	return
}

func friendlyIDEPosition(fset *token.FileSet, p token.Pos) string {
	if runtime.GOOS == "windows" {
		return fset.Position(p).String()
	}
	return filepath.Join("./", fset.Position(p).String())
}
