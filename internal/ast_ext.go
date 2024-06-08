package internal

import (
	"go/ast"
	"go/token"
)

type Def struct {
	ident *ast.Ident
	value ast.Expr
}

type PosToDefMap map[ /*ident pos*/ token.Pos]Def
