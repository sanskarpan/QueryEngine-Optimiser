package logical

import (
	"fmt"
	"strings"
)

// PrintPlan returns an indented tree string of the logical plan.
func PrintPlan(p Plan) string {
	var sb strings.Builder
	printNode(&sb, p, 0)
	return strings.TrimRight(sb.String(), "\n")
}

func printNode(sb *strings.Builder, p Plan, depth int) {
	ind := strings.Repeat("  ", depth)
	sb.WriteString(fmt.Sprintf("%s%s\n", ind, p.String()))
	for _, child := range p.Children() {
		printNode(sb, child, depth+1)
	}
}
