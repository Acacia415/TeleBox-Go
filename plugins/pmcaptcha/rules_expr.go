package pmcaptcha

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Acacia415/TeleBox-Go/internal/telegram"
)

type ruleContext struct {
	Text    string
	User    telegram.User
	Message telegram.Message
}

type ruleValue struct {
	kind string
	b    bool
	s    string
	i    int64
}

func validateRule(expression string) error {
	_, err := parseRule(expression)
	return err
}

func evaluateRule(expression string, data ruleContext) (bool, error) {
	node, err := parseRule(expression)
	if err != nil {
		return false, err
	}
	value, err := evalRuleNode(node, data)
	if err != nil {
		return false, err
	}
	if value.kind != "bool" {
		return false, errorsRule("规则结果必须为布尔值")
	}
	return value.b, nil
}

func parseRule(expression string) (ast.Expr, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, errorsRule("规则不能为空")
	}
	expression, err := normalizeRuleStrings(expression)
	if err != nil {
		return nil, err
	}
	node, err := parser.ParseExpr(normalizeRuleOperators(expression))
	if err != nil {
		return nil, fmt.Errorf("规则语法无效: %w", err)
	}
	if err := validateRuleSyntax(node); err != nil {
		return nil, err
	}
	if _, err := evalRuleNode(node, ruleContext{}); err != nil &&
		!strings.Contains(err.Error(), "正则表达式") {
		return nil, err
	}
	return node, nil
}

func normalizeRuleStrings(value string) (string, error) {
	var result strings.Builder
	characters := []rune(value)
	for index := 0; index < len(characters); {
		if characters[index] == '"' {
			start := index
			index++
			escaped := false
			for index < len(characters) {
				current := characters[index]
				index++
				if escaped {
					escaped = false
				} else if current == '\\' {
					escaped = true
				} else if current == '"' {
					break
				}
			}
			if index > len(characters) || characters[index-1] != '"' {
				return "", errorsRule("字符串没有结束引号")
			}
			result.WriteString(string(characters[start:index]))
			continue
		}
		if characters[index] != '\'' {
			result.WriteRune(characters[index])
			index++
			continue
		}
		index++
		var content strings.Builder
		escaped := false
		closed := false
		for index < len(characters) {
			current := characters[index]
			index++
			if escaped {
				switch current {
				case '\'', '\\':
					content.WriteRune(current)
				default:
					content.WriteRune('\\')
					content.WriteRune(current)
				}
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == '\'' {
				closed = true
				break
			}
			content.WriteRune(current)
		}
		if !closed {
			return "", errorsRule("字符串没有结束引号")
		}
		result.WriteString(strconv.Quote(content.String()))
	}
	return result.String(), nil
}

func validateRuleSyntax(node ast.Expr) error {
	switch value := node.(type) {
	case *ast.ParenExpr:
		return validateRuleSyntax(value.X)
	case *ast.BasicLit:
		if value.Kind != token.STRING && value.Kind != token.INT {
			return errorsRule("仅支持字符串、整数和布尔值")
		}
		return nil
	case *ast.Ident:
		if !oneOf(value.Name, "true", "false", "text") {
			return errorsRule("不支持的名称: " + value.Name)
		}
		return nil
	case *ast.SelectorExpr:
		root, ok := value.X.(*ast.Ident)
		if !ok {
			return errorsRule("字段写法无效")
		}
		_, err := ruleField(root.Name, value.Sel.Name, ruleContext{})
		return err
	case *ast.UnaryExpr:
		if value.Op != token.NOT {
			return errorsRule("仅支持 ! 取反运算")
		}
		return validateRuleSyntax(value.X)
	case *ast.BinaryExpr:
		if err := validateRuleSyntax(value.X); err != nil {
			return err
		}
		return validateRuleSyntax(value.Y)
	case *ast.CallExpr:
		name, ok := value.Fun.(*ast.Ident)
		if !ok || !oneOf(
			name.Name,
			"contains",
			"has_prefix",
			"has_suffix",
			"equals_fold",
			"matches",
		) {
			return errorsRule("不支持的函数")
		}
		for _, argument := range value.Args {
			if err := validateRuleSyntax(argument); err != nil {
				return err
			}
		}
		return nil
	default:
		return errorsRule("规则包含不支持的语法")
	}
}

func evalRuleNode(node ast.Expr, data ruleContext) (ruleValue, error) {
	switch value := node.(type) {
	case *ast.ParenExpr:
		return evalRuleNode(value.X, data)
	case *ast.BasicLit:
		switch value.Kind {
		case token.STRING:
			text, err := strconv.Unquote(value.Value)
			if err != nil {
				return ruleValue{}, errorsRule("字符串格式无效")
			}
			return ruleValue{kind: "string", s: text}, nil
		case token.INT:
			number, err := strconv.ParseInt(value.Value, 10, 64)
			if err != nil {
				return ruleValue{}, errorsRule("整数格式无效")
			}
			return ruleValue{kind: "int", i: number}, nil
		default:
			return ruleValue{}, errorsRule("仅支持字符串、整数和布尔值")
		}
	case *ast.Ident:
		switch value.Name {
		case "true":
			return ruleValue{kind: "bool", b: true}, nil
		case "false":
			return ruleValue{kind: "bool"}, nil
		case "text":
			return ruleValue{kind: "string", s: data.Text}, nil
		default:
			return ruleValue{}, errorsRule("不支持的名称: " + value.Name)
		}
	case *ast.SelectorExpr:
		root, ok := value.X.(*ast.Ident)
		if !ok {
			return ruleValue{}, errorsRule("字段写法无效")
		}
		return ruleField(root.Name, value.Sel.Name, data)
	case *ast.UnaryExpr:
		if value.Op != token.NOT {
			return ruleValue{}, errorsRule("仅支持 ! 取反运算")
		}
		item, err := evalRuleNode(value.X, data)
		if err != nil {
			return ruleValue{}, err
		}
		if item.kind != "bool" {
			return ruleValue{}, errorsRule("! 只能用于布尔值")
		}
		return ruleValue{kind: "bool", b: !item.b}, nil
	case *ast.BinaryExpr:
		return evalBinaryRule(value, data)
	case *ast.CallExpr:
		return evalRuleCall(value, data)
	default:
		return ruleValue{}, errorsRule("规则包含不支持的语法")
	}
}

func ruleField(root, field string, data ruleContext) (ruleValue, error) {
	switch root + "." + field {
	case "user.id":
		return ruleValue{kind: "int", i: data.User.ID}, nil
	case "user.username":
		return ruleValue{kind: "string", s: data.User.Username}, nil
	case "user.language":
		return ruleValue{kind: "string", s: data.User.LanguageCode}, nil
	case "user.premium":
		return ruleValue{kind: "bool", b: data.User.Premium}, nil
	case "user.verified":
		return ruleValue{kind: "bool", b: data.User.Verified}, nil
	case "user.contact":
		return ruleValue{kind: "bool", b: data.User.Contact}, nil
	case "user.mutual_contact":
		return ruleValue{kind: "bool", b: data.User.MutualContact}, nil
	case "user.bot":
		return ruleValue{kind: "bool", b: data.User.Bot}, nil
	case "message.outgoing":
		return ruleValue{kind: "bool", b: data.Message.Outgoing}, nil
	case "message.has_media":
		return ruleValue{kind: "bool", b: data.Message.Media != nil}, nil
	case "message.has_sticker":
		return ruleValue{kind: "bool", b: data.Message.Sticker != nil}, nil
	default:
		return ruleValue{}, errorsRule("不支持的字段: " + root + "." + field)
	}
}

func evalBinaryRule(node *ast.BinaryExpr, data ruleContext) (ruleValue, error) {
	left, err := evalRuleNode(node.X, data)
	if err != nil {
		return ruleValue{}, err
	}
	if node.Op == token.LAND || node.Op == token.LOR {
		right, err := evalRuleNode(node.Y, data)
		if err != nil {
			return ruleValue{}, err
		}
		if left.kind != "bool" {
			return ruleValue{}, errorsRule("逻辑运算两侧必须为布尔值")
		}
		if right.kind != "bool" {
			return ruleValue{}, errorsRule("逻辑运算两侧必须为布尔值")
		}
		if node.Op == token.LAND {
			return ruleValue{kind: "bool", b: left.b && right.b}, nil
		}
		return ruleValue{kind: "bool", b: left.b || right.b}, nil
	}
	right, err := evalRuleNode(node.Y, data)
	if err != nil {
		return ruleValue{}, err
	}
	if left.kind != right.kind {
		return ruleValue{}, errorsRule("比较两侧的数据类型必须一致")
	}
	var result bool
	switch left.kind {
	case "string":
		switch node.Op {
		case token.EQL:
			result = left.s == right.s
		case token.NEQ:
			result = left.s != right.s
		default:
			return ruleValue{}, errorsRule("字符串仅支持 == 和 !=")
		}
	case "int":
		switch node.Op {
		case token.EQL:
			result = left.i == right.i
		case token.NEQ:
			result = left.i != right.i
		case token.GTR:
			result = left.i > right.i
		case token.GEQ:
			result = left.i >= right.i
		case token.LSS:
			result = left.i < right.i
		case token.LEQ:
			result = left.i <= right.i
		default:
			return ruleValue{}, errorsRule("整数比较符无效")
		}
	case "bool":
		switch node.Op {
		case token.EQL:
			result = left.b == right.b
		case token.NEQ:
			result = left.b != right.b
		default:
			return ruleValue{}, errorsRule("布尔值仅支持 == 和 !=")
		}
	default:
		return ruleValue{}, errorsRule("不支持的数据类型")
	}
	return ruleValue{kind: "bool", b: result}, nil
}

func evalRuleCall(node *ast.CallExpr, data ruleContext) (ruleValue, error) {
	name, ok := node.Fun.(*ast.Ident)
	if !ok {
		return ruleValue{}, errorsRule("函数写法无效")
	}
	if len(node.Args) != 2 {
		return ruleValue{}, errorsRule(name.Name + " 需要两个参数")
	}
	first, err := evalRuleNode(node.Args[0], data)
	if err != nil {
		return ruleValue{}, err
	}
	second, err := evalRuleNode(node.Args[1], data)
	if err != nil {
		return ruleValue{}, err
	}
	if first.kind != "string" || second.kind != "string" {
		return ruleValue{}, errorsRule(name.Name + " 的参数必须是字符串")
	}
	var result bool
	switch name.Name {
	case "contains":
		result = strings.Contains(first.s, second.s)
	case "has_prefix":
		result = strings.HasPrefix(first.s, second.s)
	case "has_suffix":
		result = strings.HasSuffix(first.s, second.s)
	case "equals_fold":
		result = strings.EqualFold(first.s, second.s)
	case "matches":
		expression, err := regexp.Compile(second.s)
		if err != nil {
			return ruleValue{}, fmt.Errorf("正则表达式无效: %w", err)
		}
		result = expression.MatchString(first.s)
	default:
		return ruleValue{}, errorsRule("不支持的函数: " + name.Name)
	}
	return ruleValue{kind: "bool", b: result}, nil
}

func normalizeRuleOperators(value string) string {
	var result strings.Builder
	var word strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		tokenValue := word.String()
		switch tokenValue {
		case "and":
			tokenValue = "&&"
		case "or":
			tokenValue = "||"
		case "not":
			tokenValue = "!"
		}
		result.WriteString(tokenValue)
		word.Reset()
	}
	for _, character := range value {
		if quote != 0 {
			result.WriteRune(character)
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		if character == '"' || character == '\'' {
			flush()
			quote = character
			result.WriteRune(character)
			continue
		}
		if unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == '_' {
			word.WriteRune(character)
			continue
		}
		flush()
		result.WriteRune(character)
	}
	flush()
	return result.String()
}

func errorsRule(message string) error {
	return fmt.Errorf("规则无效: %s", message)
}
