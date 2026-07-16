package ast

// Visitor walks an AST. Implement this interface to traverse the tree.
type Visitor interface {
	VisitSelectStatement(*SelectStatement) error
	VisitCreateTableStatement(*CreateTableStatement) error
	VisitInsertStatement(*InsertStatement) error

	VisitStarExpr(*StarExpr) error
	VisitAliasExpr(*AliasExpr) error
	VisitColumnRef(*ColumnRef) error
	VisitIntLiteral(*IntLiteral) error
	VisitFloatLiteral(*FloatLiteral) error
	VisitStringLiteral(*StringLiteral) error
	VisitBoolLiteral(*BoolLiteral) error
	VisitNullLiteral(*NullLiteral) error
	VisitBinaryExpr(*BinaryExpr) error
	VisitUnaryExpr(*UnaryExpr) error
	VisitFunctionCall(*FunctionCall) error
	VisitCaseExpr(*CaseExpr) error
	VisitInExpr(*InExpr) error
	VisitBetweenExpr(*BetweenExpr) error
	VisitIsNullExpr(*IsNullExpr) error
	VisitSubqueryExpr(*SubqueryExpr) error
	VisitExistsExpr(*ExistsExpr) error
}

// Walk dispatches to the correct visitor method.
func Walk(v Visitor, node interface{}) error {
	switch n := node.(type) {
	case *SelectStatement:
		return v.VisitSelectStatement(n)
	case *CreateTableStatement:
		return v.VisitCreateTableStatement(n)
	case *InsertStatement:
		return v.VisitInsertStatement(n)
	case *StarExpr:
		return v.VisitStarExpr(n)
	case *AliasExpr:
		return v.VisitAliasExpr(n)
	case *ColumnRef:
		return v.VisitColumnRef(n)
	case *IntLiteral:
		return v.VisitIntLiteral(n)
	case *FloatLiteral:
		return v.VisitFloatLiteral(n)
	case *StringLiteral:
		return v.VisitStringLiteral(n)
	case *BoolLiteral:
		return v.VisitBoolLiteral(n)
	case *NullLiteral:
		return v.VisitNullLiteral(n)
	case *BinaryExpr:
		return v.VisitBinaryExpr(n)
	case *UnaryExpr:
		return v.VisitUnaryExpr(n)
	case *FunctionCall:
		return v.VisitFunctionCall(n)
	case *CaseExpr:
		return v.VisitCaseExpr(n)
	case *InExpr:
		return v.VisitInExpr(n)
	case *BetweenExpr:
		return v.VisitBetweenExpr(n)
	case *IsNullExpr:
		return v.VisitIsNullExpr(n)
	case *SubqueryExpr:
		return v.VisitSubqueryExpr(n)
	case *ExistsExpr:
		return v.VisitExistsExpr(n)
	}
	return nil
}
