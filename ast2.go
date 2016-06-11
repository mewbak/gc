// Copyright 2016 The GC Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gc

import (
	"go/token"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cznic/xc"
)

// Node represents the interface all AST nodes, xc.Token and lex.Char implement.
type Node interface {
	Pos() token.Pos
}

// ------------------------------------------------------------------- Argument

func (n *Argument) ident() (t xc.Token) {
	if n.Case != 0 { // Expression
		return t
	}

	return n.Expression.ident()
}

// ------------------------------------------------------------------ ConstSpec

func (n *ConstSpec) decl(lx *lexer, t *Typ, el *ExpressionList) {
	for l := n.IdentifierList; l != nil; l = l.IdentifierList {
		d := newConstDeclaration(l.ident(), lx.lookahead.Pos())
		lx.scope.declare(lx, d)
	}
}

// ----------------------------------------------------------------- Expression

func (n *Expression) ident() (t xc.Token) {
	if n.Case != 0 { // UnaryExpression
		return t
	}

	u := n.UnaryExpression
	if u.Case != 7 { // PrimaryExpression
		return t
	}

	p := u.PrimaryExpression
	if p.Case != 0 { // Operand
		return t
	}

	o := p.Operand
	if o.Case != 4 || o.GenericArgumentsOpt != nil { // IDENTIFIER GenericArgumentsOpt
		return t
	}

	return o.Token
}

// ------------------------------------------------------------- IdentifierList

func (n *IdentifierList) ident() xc.Token {
	if n.Case == 0 { // IDENTIFIER
		return n.Token
	}

	// case 1: // IdentifierList ',' IDENTIFIER
	return n.Token2

}

// ----------------------------------------------------------------- ImportSpec

func (n *ImportSpec) post(lx *lexer) {
	var nm int
	var pos token.Pos
	if o := n.IdentifierOpt; o != nil {
		nm = o.Token.Val
		pos = o.Token.Pos()
	}
	var ip string
	var bl *BasicLiteral
	switch bl = n.BasicLiteral; bl.Case {
	case
		0, //CHAR_LIT
		1, //FLOAT_LIT
		2, //IMAG_LIT
		3: //INT_LIT
		lx.err(bl, "import statement requires a string")
		return
	case 4: //STRING_LIT
		if !pos.IsValid() {
			pos = bl.Pos()
		}
		val := bl.val
		if val == nil {
			return
		}

		ip = val.(StringID).String()
		if ip == "" {
			lx.err(bl, "import path is empty")
			return
		}

		if ip[0] == '/' {
			lx.err(bl, "import path cannot be absolute path")
			return
		}

		for _, r := range ip {
			if strings.ContainsAny(ip, "!\"#$%&'()*,:;<=>?[\\]^`{|}") || r == '\ufffd' {
				lx.err(bl, "import path contains invalid character")
				return
			}

			if !unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S) {
				lx.err(bl, "invalid import path")
				return
			}
		}

		if isRelativeImportPath(ip) {
			pi := lx.pkg.ImportPath
			if t := lx.test; t != nil {
				if _, ok := t.pkgMap[pi]; ok {
					pi = filepath.Dir(pi)
				}
			}
			ip = filepath.Join(pi, ip)
		}
	default:
		panic("internal error")
	}
	lx.imports = append(lx.imports, newImportDeclaration(lx.pkg, nm, pos, lx.oncePackage(bl, ip))) // Async load.
}

// -------------------------------------------------------------- PackageClause

func (n *PackageClause) post(lx *lexer) {
	p := lx.pkg
	t := n.Token2
	nm := t.Val
	switch {
	case p.Name != "":
		if p.name != nm {
			lx.close(t,
				"package %s: found packages %s (%s) and %s (%s) in %s",
				p.ImportPath,
				p.Name, filepath.Base(p.namedBy),
				t.S(), filepath.Base(lx.name),
				p.Directory,
			)
			return
		}
	default:
		p.Name = string(t.S())
		p.name = nm
		p.namedBy = lx.name
	}
}

// ----------------------------------------------------------------- Parameters

func (n *Parameters) post(lx *lexer) {
	hasNamedParams := false
	i := 0
	for l := n.ParameterDeclList; l != nil; l = l.ParameterDeclList {
		pd := l.ParameterDecl
		n.list = append(n.list, pd)
		switch pd.Case {
		case 0: // "..." Typ
			if l.ParameterDeclList != nil {
				lx.err(pd, "can only use ... as final argument in list")
			}

			if hasNamedParams {
				lx.err(pd, "mixed named and unnamed parameters")
			}
		case 1: // IDENTIFIER "..." Typ
			hasNamedParams = true
			pd.nm = pd.Token
			if l.ParameterDeclList != nil {
				lx.err(pd, "can only use ... as final argument in list")
			}

			for j := i - 1; j >= 0 && n.list[j].Case == 3; j-- {
				n.list[j].isParamName = true
			}
		case 2: // IDENTIFIER Typ
			hasNamedParams = true
			pd.nm = pd.Token
			for j := i - 1; j >= 0 && n.list[j].Case == 3; j-- {
				n.list[j].isParamName = true
			}
		case 3: // Typ
			switch t := pd.Typ; t.Case {
			case 7: // QualifiedIdent GenericArgumentsOpt
				switch qi := t.QualifiedIdent; qi.Case {
				case 0: // IDENTIFIER
					pd.nm = qi.Token
				}
			}
		}
		i++
	}
	if !hasNamedParams {
		return
	}

	for _, v := range n.list {
		nm := v.nm
		if nm.IsValid() {
			if lx.scope.Bindings[nm.Val] != nil {
				lx.err(v, "duplicate argument %s", nm.S())
				continue
			}

			lx.scope.declare(lx, newVarDeclaration(nm, nm.Pos())) //TODO Must adjust scopeStart to block beginning.
			continue
		}

		if v.isParamName && !nm.IsValid() {
			lx.err(v, "mixed named and unnamed parameters")
		}
	}
}

// ------------------------------------------------------------------- Prologue

func (n *Prologue) post(lx *lexer) {
	for _, v := range lx.imports {
		p := v.once.Value().(*Package)
		v.Package = p
		switch v.Name() {
		case idDot:
			lx.dotImports = append(lx.dotImports, v)
			for _, v := range p.Scope.Bindings {
				p.Scope.declare(lx, v)
			}
			continue
		case idUnderscore:
			lx.unboundImports = append(lx.unboundImports, v)
			continue
		case 0:
			switch {
			case p.ImportPath == "C":
				v.name = idC
			default:
				v.name = p.name
			}
		}

		lx.fileScope.declare(lx, v)
	}
}

// ---------------------------------------------------------------------- Range

func (n *Range) shortVarDecl(lx *lexer) {
	if n.Case != 1 { // ExpressionList ":=" "range" Expression
		panic("internal error")
	}

	cnt := 0
	l := n.ExpressionList
	for ; l != nil && cnt < 2; l = l.ExpressionList {
		t := l.Expression.ident()
		if t.IsValid() {
			ex := lx.scope.Bindings[t.Val]
			switch {
			case ex != nil:
				lx.err(ex, "%s repeated on left side of :=", t.S())
			default:
				lx.scope.declare(lx, newVarDeclaration(t, lx.lookahead.Pos()))
			}
		}
		cnt++
	}
	if l != nil {
		lx.err(l.Expression, "too many variables declared by the range clause")
	}
}

// ----------------------------------------------------------------- SwitchCase

func (n *SwitchCase) shortVarDecl(lx *lexer) {
	if n.Case != 2 { // "case" ArgumentList ":=" Expression ':'
		panic("internal error")
	}

	cnt := 0
	l := n.ArgumentList
	for ; l != nil && cnt < 2; l = l.ArgumentList {
		t := l.Argument.ident()
		if t.IsValid() {
			ex := lx.scope.Bindings[t.Val]
			switch {
			case ex != nil:
				lx.err(ex, "%s repeated on left side of :=", t.S())
			default:
				lx.scope.declare(lx, newVarDeclaration(t, lx.lookahead.Pos()))
			}
		}
		cnt++
	}
	if l != nil {
		lx.err(l.Argument, "too many variables declared by the case clause")
	}
}

// -------------------------------------------------------------------- VarSpec

func (n *VarSpec) decl(lx *lexer, t *Typ, el *ExpressionList) {
	for l := n.IdentifierList; l != nil; l = l.IdentifierList {
		lx.scope.declare(lx, newVarDeclaration(l.ident(), lx.lookahead.Pos()))
	}
}
