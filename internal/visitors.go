package internal

import (
	"github.com/grafana/regexp"
	"go/ast"
	"go/token"
	"golang.org/x/tools/go/analysis"

	"github.com/charmbracelet/log"
)

type QueryGenVisitor struct {
	pass                *analysis.Pass
	foldingState        Set[string]
	perFileDefs         map[string]PosToDefMap
	substitutionRegex   *regexp.Regexp
	queryConstNameRegex *regexp.Regexp
	structFactory       StructFactory
	logger              *log.Logger

	// ParamStructs is the list of GoStructs generated for a file.
	ParamStructs []GoStruct
}

var _ ast.Visitor = &QueryGenVisitor{}

func NewQueryGenVisitor(logger *log.Logger, pass *analysis.Pass) *QueryGenVisitor {
	return &QueryGenVisitor{
		pass:                pass,
		foldingState:        Set[string]{},
		perFileDefs:         map[string]PosToDefMap{},
		substitutionRegex:   SubstitutionRegex,
		queryConstNameRegex: QueryConstNameRegex,
		structFactory:       StructFactory{pass},
		logger:              logger,
		ParamStructs:        nil,
	}
}

type posToNodeMapperVisitor struct {
	PosToDefMap
}

func (p posToNodeMapperVisitor) Visit(node ast.Node) (w ast.Visitor) {
	if genDecl, ok := node.(*ast.GenDecl); ok {
		if genDecl.Tok == token.CONST {
			for _, spec := range genDecl.Specs {
				valSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range valSpec.Names {
					if i >= len(valSpec.Values) { // malformed code
						break
					}
					p.PosToDefMap[name.Pos()] = Def{ident: name, value: valSpec.Values[i]}
				}
			}
		}
		return nil
	}
	return p
}

func (q *QueryGenVisitor) Visit(node ast.Node) ast.Visitor {
	if genDecl, ok := node.(*ast.GenDecl); ok {
		if genDecl.Tok != token.CONST {
			return nil
		}
		for _, spec := range genDecl.Specs {
			valSpec, ok := spec.(*ast.ValueSpec)
			if !ok { // malformed syntax
				continue
			}
			for i, ident := range valSpec.Names {
				if i >= len(valSpec.Values) { // malformed code
					break
				}
				if !q.queryConstNameRegex.MatchString(ident.Name) {
					continue
				}
				queryVarName := ident.Name
				logger := q.logger.With("const", queryVarName)
				expr := valSpec.Values[i]
				func() {
					q.foldingState.Add(queryVarName)
					defer q.foldingState.Remove(queryVarName)
					logger.Debug("trying to fold Query string")
					if foldedString, ok := q.tryFoldString(expr); ok {
						logger.Debug("constant-folded Query string", "foldedString", foldedString)
						goStruct, err := q.structFactory.NewGoStruct(q.substitutionRegex, ident, foldedString)
						if err != nil {
							logger.Error("failed to create struct from query string", "err", err)
							return
						}
						if goStruct != nil {
							q.ParamStructs = append(q.ParamStructs, *goStruct)
						}
					} else {
						logger.Debug("failed to fold Query string")
					}
				}()
			}
		}
		return nil
	}
	return q
}

func (q *QueryGenVisitor) tryLocateStringForPos(pos token.Pos) (string, bool) {
	file := q.pass.Fset.File(pos)
	if file == nil {
		return "", false
	}
	var astFile *ast.File
	for _, f := range q.pass.Files {
		if f.FileStart <= pos && pos < f.FileEnd {
			astFile = f
			break
		}
	}
	if astFile == nil {
		q.pass.Reportf(token.NoPos, "failed to locate astFile for: %v", file.Name())
		return "", false
	}
	if q.perFileDefs == nil {
		q.perFileDefs = make(map[string]PosToDefMap)
	}
	fileDefPosMap, ok := q.perFileDefs[file.Name()]
	if !ok {
		visitor := &posToNodeMapperVisitor{PosToDefMap{}}
		ast.Walk(visitor, astFile)
		fileDefPosMap = visitor.PosToDefMap
		q.perFileDefs[file.Name()] = fileDefPosMap
	}
	if posDef, ok := fileDefPosMap[pos]; ok {
		return q.tryFoldString(posDef.value)
	}
	return "", false
}

func (q *QueryGenVisitor) tryFoldString(expr ast.Expr) (string, bool) {
	switch expr.(type) {
	case *ast.BasicLit:
		basicLit := expr.(*ast.BasicLit)
		if basicLit.Kind != token.STRING {
			return "", false
		}
		return basicLit.Value, true
	case *ast.BinaryExpr:
		binaryExpr := expr.(*ast.BinaryExpr)
		lhs, ok := q.tryFoldString(binaryExpr.X)
		if !ok {
			return "", false
		}
		rhs, ok := q.tryFoldString(binaryExpr.Y)
		if !ok {
			return "", false
		}
		return lhs + rhs, true
	case *ast.Ident:
		ident := expr.(*ast.Ident)

		if q.foldingState.Has(ident.Name) {
			q.logger.Warn("cyclic dependency in constant expression", "ident", ident.Name)
			return "", false
		}
		q.foldingState.Add(ident.Name)
		defer q.foldingState.Remove(ident.Name)

		object := q.pass.TypesInfo.ObjectOf(ident)
		if object == nil {
			return "", false
		}
		pos := object.Pos()
		return q.tryLocateStringForPos(pos)
	default:
		return "", false
	}
}
