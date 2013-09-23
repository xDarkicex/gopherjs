package main

import (
	"code.google.com/p/go.tools/go/exact"
	"code.google.com/p/go.tools/go/types"
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

func (c *PkgContext) translateStmtList(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		c.translateStmt(stmt, "")
	}
}

func (c *PkgContext) translateStmt(stmt ast.Stmt, label string) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		c.Printf("{")
		c.Indent(func() {
			c.translateStmtList(s.List)
		})
		c.Printf("}")

	case *ast.IfStmt:
		c.translateStmt(s.Init, "")
		c.Printf("if (%s) {", c.translateExpr(s.Cond))
		c.Indent(func() {
			c.translateStmtList(s.Body.List)
		})
		if s.Else != nil {
			c.Printf("} else")
			c.translateStmt(s.Else, "")
			return
		}
		c.Printf("}")

	case *ast.SwitchStmt:
		c.translateStmt(s.Init, "")
		condPrefix := ""
		if s.Tag != nil {
			refVar := c.newVarName("_ref")
			c.Printf("var %s = %s;", refVar, c.translateExpr(s.Tag))
			condPrefix = refVar + " === "
		}
		c.translateSwitch(s.Body.List, false, condPrefix, label)

	case *ast.TypeSwitchStmt:
		c.translateStmt(s.Init, "")
		expr := ""
		if assign, isAssign := s.Assign.(*ast.AssignStmt); isAssign {
			id := assign.Lhs[0].(*ast.Ident)
			expr = c.newVarName(id.Name)
			obj := &types.Var{}
			c.info.Objects[id] = obj
			c.objectVars[obj] = expr
			c.translateStmt(s.Assign, "")
			for _, caseClause := range s.Body.List {
				c.objectVars[c.info.Implicits[caseClause]] = expr
			}
		}
		if expr == "" {
			expr = c.translateExpr(s.Assign.(*ast.ExprStmt).X)
		}
		typeVar := c.newVarName("_type")
		c.Printf("var %s = typeOf(%s);", typeVar, expr)
		condPrefix := typeVar + " === "
		c.translateSwitch(s.Body.List, true, condPrefix, label)

	case *ast.ForStmt:
		c.translateStmt(s.Init, "")
		post := strings.TrimSuffix(strings.TrimSpace(c.CatchOutput(func() { c.translateStmt(s.Post, "") })), ";") // TODO ugly
		c.Printf("%sfor (; %s; %s) {", label, c.translateExpr(s.Cond), post)
		c.Indent(func() {
			c.translateStmtList(s.Body.List)
		})
		c.Printf("}")

	case *ast.RangeStmt:
		key := ""
		if s.Key != nil && !isUnderscore(s.Key) {
			key = c.translateExpr(s.Key)
		}
		value := ""
		if s.Value != nil && !isUnderscore(s.Value) {
			value = c.translateExpr(s.Value)
		}
		varKeyword := ""
		if s.Tok == token.DEFINE {
			varKeyword = "var "
		}

		refVar := c.newVarName("_ref")
		c.Printf("var %s = %s;", refVar, c.translateExpr(s.X))

		lenTarget := refVar
		_, isMap := c.info.Types[s.X].Underlying().(*types.Map)
		var keysVar string
		if isMap {
			keysVar = c.newVarName("_keys")
			c.Printf("var %s = %s !== null ? Object.keys(%s) : [];", keysVar, refVar, refVar)
			lenTarget = keysVar
		}

		lenVar := c.newVarName("_len")
		c.Printf("var %s = %s.length;", lenVar, lenTarget)

		iVar := c.newVarName("_i")
		c.Printf("var %s = 0;", iVar)

		c.Printf("%sfor (; %s < %s; %s++) {", label, iVar, lenVar, iVar)
		c.Indent(func() {
			var entryVar string
			if isMap {
				entryVar = c.newVarName("_entry")
				c.Printf("var %s = %s[%s[%s]];", entryVar, refVar, keysVar, iVar)
				if key != "" {
					c.Printf("%s%s = %s.k;", varKeyword, key, entryVar)
				}
			}
			if !isMap && key != "" {
				c.Printf("%s%s = %s;", varKeyword, key, iVar)
			}
			if value != "" {
				switch t := c.info.Types[s.X].Underlying().(type) {
				case *types.Array:
					c.Printf("%s%s = %s[%s];", varKeyword, value, refVar, iVar)
				case *types.Slice:
					c.Printf("%s%s = %s.get(%s);", varKeyword, value, refVar, iVar)
				case *types.Map:
					c.Printf("%s%s = %s.v;", varKeyword, value, entryVar)
				case *types.Basic:
					c.Printf("%s%s = %s.charCodeAt(%s);", varKeyword, value, refVar, iVar)
				default:
					panic(fmt.Sprintf("Unhandled range type: %T\n", t))
				}
			}
			c.translateStmtList(s.Body.List)
		})
		c.Printf("}")

	case *ast.BranchStmt:
		label := ""
		if s.Label != nil {
			label = " " + s.Label.Name
		}
		switch s.Tok {
		case token.BREAK:
			c.Printf("break%s;", label)
		case token.CONTINUE:
			c.Printf("continue%s;", label)
		case token.GOTO:
			c.Printf(`throw "goto not implemented";`)
		case token.FALLTHROUGH:
			// handled in CaseClause
		default:
			panic("Unhandled branch statment: " + s.Tok.String())
		}

	case *ast.ReturnStmt:
		results := make([]string, len(s.Results))
		for i, result := range s.Results {
			results[i] = c.translateExpr(result)
			if c.namedResults != nil {
				c.Printf("%s = %s;", c.namedResults[i], results[i])
			}
		}
		if c.namedResults != nil {
			results = c.namedResults
		}
		switch len(results) {
		case 0:
			c.Printf("return;")
		case 1:
			c.Printf("return %s;", results[0])
		default:
			c.Printf("return [%s];", strings.Join(results, ", "))
		}

	case *ast.DeferStmt:
		args := c.translateArgs(s.Call)
		c.Printf("_deferred.push({ fun: %s, recv: %s, args: [%s] });", c.translateExpr(s.Call.Fun), "this", strings.Join(args, ", ")) // TODO fix receiver

	case *ast.ExprStmt:
		c.Printf("%s;", c.translateExpr(s.X))

	case *ast.DeclStmt:
		for _, spec := range s.Decl.(*ast.GenDecl).Specs {
			c.translateSpec(spec)
		}

	case *ast.LabeledStmt:
		c.translateStmt(s.Stmt, s.Label.Name+": ")

	case *ast.AssignStmt:
		rhsExprs := make([]string, len(s.Lhs))

		switch {
		case len(s.Lhs) == 1 && len(s.Rhs) == 1:
			rhsExprs[0] = c.translateExprToType(s.Rhs[0], c.info.Types[s.Lhs[0]])

		case len(s.Lhs) > 1 && len(s.Rhs) == 1:
			for i := range s.Lhs {
				rhsExprs[i] = fmt.Sprintf("_tuple[%d]", i)
			}
			c.Printf("_tuple = %s;", c.translateExpr(s.Rhs[0])) // TODO translateExprToType

		case len(s.Lhs) == len(s.Rhs):
			parts := make([]string, len(s.Rhs))
			for i, rhs := range s.Rhs {
				parts[i] = c.translateExprToType(rhs, c.info.Types[s.Lhs[i]])
				rhsExprs[i] = fmt.Sprintf("_tuple[%d]", i)
			}
			c.Printf("_tuple = [%s];", strings.Join(parts, ", "))

		default:
			panic("Invalid arity of AssignStmt.")

		}

		for i, lhs := range s.Lhs {
			rhs := rhsExprs[i]
			if isUnderscore(lhs) {
				if len(s.Lhs) == 1 {
					c.Printf("%s;", rhs)
				}
				continue
			}

			if s.Tok == token.DEFINE {
				c.Printf("var %s = %s;", c.translateExpr(lhs), rhs)
				continue
			}

			switch l := lhs.(type) {
			case *ast.StarExpr:
				if _, isStruct := c.info.Types[l].(*types.Struct); !isStruct {
					c.Printf("%s.set(%s);", c.translateExpr(l.X), rhs)
					continue
				}
			case *ast.IndexExpr:
				switch t := c.info.Types[l.X].Underlying().(type) {
				case *types.Slice:
					c.Printf("%s.set(%s, %s);", c.translateExpr(l.X), c.translateExpr(l.Index), rhs)
					continue
				case *types.Map:
					index := c.translateExpr(l.Index)
					if _, isPointer := t.Key().Underlying().(*types.Pointer); isPointer {
						index = fmt.Sprintf("(%s || Go$Nil)._id", index)
					}
					keyVar := c.newVarName("_key")
					c.Printf("var %s = %s;", keyVar, index)
					c.Printf("%s[%s] = { k: %s, v: %s };", c.translateExpr(l.X), keyVar, keyVar, rhs)
					continue
				}
			}

			tok := s.Tok.String()
			if s.Tok == token.AND_NOT_ASSIGN {
				tok = "&=~"
			}
			c.Printf("%s %s %s;", c.translateExpr(lhs), tok, rhs)
		}

	case *ast.IncDecStmt:
		if iExpr, isIExpr := s.X.(*ast.IndexExpr); isIExpr {
			if _, isMap := c.info.Types[iExpr.X].Underlying().(*types.Map); isMap {
				op := token.ADD
				if s.Tok == token.DEC {
					op = token.SUB
				}
				one := &ast.BasicLit{
					Kind:  token.INT,
					Value: "1",
				}
				c.info.Values[one] = exact.MakeInt64(1)
				c.translateStmt(&ast.AssignStmt{
					Lhs: []ast.Expr{s.X},
					Tok: token.ASSIGN,
					Rhs: []ast.Expr{
						&ast.BinaryExpr{
							X:  s.X,
							Op: op,
							Y:  one,
						},
					},
				}, label)
				return
			}
		}
		c.Printf("%s%s;", c.translateExpr(s.X), s.Tok)

	case *ast.SelectStmt, *ast.GoStmt, *ast.SendStmt:
		c.Printf(`throw new GoError("Statement not supported: %T");`, s)

	case nil:
		// skip

	default:
		panic(fmt.Sprintf("Unhandled statement: %T\n", s))

	}
}

func (c *PkgContext) translateSwitch(caseClauses []ast.Stmt, typeSwitch bool, condPrefix string, label string) {
	if len(caseClauses) == 0 {
		return
	}
	if len(caseClauses) == 1 && caseClauses[0].(*ast.CaseClause).List == nil {
		c.translateStmtList(caseClauses[0].(*ast.CaseClause).Body)
		return
	}

	clauseStmts := make([][]ast.Stmt, len(caseClauses))
	openClauses := make([]int, 0)
	for i, child := range caseClauses {
		caseClause := child.(*ast.CaseClause)
		openClauses = append(openClauses, i)
		for _, j := range openClauses {
			clauseStmts[j] = append(clauseStmts[j], caseClause.Body...)
		}
		if !hasFallthrough(caseClause) {
			openClauses = nil
		}
	}

	c.Printf("%sdo {", label)
	c.Indent(func() {
		var defaultClause []ast.Stmt
		for i, child := range caseClauses {
			caseClause := child.(*ast.CaseClause)
			if len(caseClause.List) == 0 {
				defaultClause = clauseStmts[i]
				continue
			}
			conds := make([]string, len(caseClause.List))
			for i, cond := range caseClause.List {
				if typeSwitch {
					conds[i] = condPrefix + c.typeName(c.info.Types[cond])
					continue
				}
				conds[i] = condPrefix + c.translateExpr(cond)
			}
			c.Printf("if (%s) {", strings.Join(conds, " || "))
			c.Indent(func() {
				c.translateStmtList(clauseStmts[i])
			})
			if i < len(caseClauses)-1 || defaultClause != nil {
				c.Printf("} else")
				continue
			}
			c.Printf("}")
		}
		c.Printf("{")
		c.Indent(func() {
			c.translateStmtList(defaultClause)
		})
		c.Printf("}")
	})
	c.Printf("} while (false);")
}

func hasFallthrough(caseClause *ast.CaseClause) bool {
	if len(caseClause.Body) == 0 {
		return false
	}
	b, isBranchStmt := caseClause.Body[len(caseClause.Body)-1].(*ast.BranchStmt)
	return isBranchStmt && b.Tok == token.FALLTHROUGH
}
