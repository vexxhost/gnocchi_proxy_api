package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
)

type Predicate interface {
	Match(*gnocchi.Resource) bool
}

type PredicateFunc func(*gnocchi.Resource) bool

func (f PredicateFunc) Match(resource *gnocchi.Resource) bool { return f(resource) }

func ParseJSONFilter(raw []byte) (Predicate, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return PredicateFunc(func(*gnocchi.Resource) bool { return true }), nil
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return compilePredicate(data)
}

func compilePredicate(data any) (Predicate, error) {
	object, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("filter must be a JSON object")
	}
	if len(object) != 1 {
		return nil, fmt.Errorf("filter object must contain exactly one operator")
	}
	for operator, operand := range object {
		switch operator {
		case "and", "or":
			items, ok := operand.([]any)
			if !ok {
				return nil, fmt.Errorf("%s requires an array", operator)
			}
			predicates := make([]Predicate, 0, len(items))
			for _, item := range items {
				predicate, err := compilePredicate(item)
				if err != nil {
					return nil, err
				}
				predicates = append(predicates, predicate)
			}
			if operator == "and" {
				return PredicateFunc(func(resource *gnocchi.Resource) bool {
					for _, predicate := range predicates {
						if !predicate.Match(resource) {
							return false
						}
					}
					return true
				}), nil
			}
			return PredicateFunc(func(resource *gnocchi.Resource) bool {
				for _, predicate := range predicates {
					if predicate.Match(resource) {
						return true
					}
				}
				return false
			}), nil
		case "=", "!=", "like", ">", ">=", "<", "<=", "in":
			fieldMap, ok := operand.(map[string]any)
			if !ok || len(fieldMap) != 1 {
				return nil, fmt.Errorf("%s requires a single field object", operator)
			}
			for field, value := range fieldMap {
				return compileComparison(operator, field, value)
			}
		}
		return nil, fmt.Errorf("unsupported filter operator %q", operator)
	}
	return nil, fmt.Errorf("empty filter")
}

func compileComparison(operator, field string, value any) (Predicate, error) {
	return PredicateFunc(func(resource *gnocchi.Resource) bool {
		actual := resourceValue(resource, field)
		switch operator {
		case "=":
			return compare(actual, value) == 0
		case "!=":
			return compare(actual, value) != 0
		case "like":
			return likeMatch(fmt.Sprint(actual), fmt.Sprint(value))
		case ">":
			return compare(actual, value) > 0
		case ">=":
			return compare(actual, value) >= 0
		case "<":
			return compare(actual, value) < 0
		case "<=":
			return compare(actual, value) <= 0
		case "in":
			items, ok := value.([]any)
			if !ok {
				return false
			}
			for _, item := range items {
				if compare(actual, item) == 0 {
					return true
				}
			}
			return false
		default:
			return false
		}
	}), nil
}

func resourceValue(resource *gnocchi.Resource, field string) any {
	switch field {
	case "id":
		return resource.ID
	case "type", "resource_type":
		return resource.Type
	default:
		return resource.Attrs[field]
	}
}

func compare(left any, right any) int {
	lf, lok := gnocchi.ParseFloat(left)
	rf, rok := gnocchi.ParseFloat(right)
	if lok && rok {
		switch {
		case lf < rf:
			return -1
		case lf > rf:
			return 1
		default:
			return 0
		}
	}

	ls := normalizeString(left)
	rs := normalizeString(right)
	switch {
	case ls < rs:
		return -1
	case ls > rs:
		return 1
	default:
		return 0
	}
}

func likeMatch(actual string, pattern string) bool {
	parts := strings.Split(pattern, "%")
	if len(parts) == 1 {
		return actual == pattern
	}
	index := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		pos := strings.Index(actual[index:], part)
		if pos < 0 {
			return false
		}
		if i == 0 && !strings.HasPrefix(pattern, "%") && pos != 0 {
			return false
		}
		index += pos + len(part)
	}
	if !strings.HasSuffix(pattern, "%") {
		last := parts[len(parts)-1]
		return strings.HasSuffix(actual, last)
	}
	return true
}

func normalizeString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func ParseFlatFilter(raw string) (Predicate, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PredicateFunc(func(*gnocchi.Resource) bool { return true }), nil
	}
	tokens, err := tokenizeFilter(raw)
	if err != nil {
		return nil, err
	}
	parser := &filterParser{tokens: tokens}
	return parser.parse()
}

type filterToken struct {
	kind  string
	value string
}

func tokenizeFilter(raw string) ([]filterToken, error) {
	tokens := make([]filterToken, 0)
	for i := 0; i < len(raw); {
		switch ch := raw[i]; {
		case ch == ' ' || ch == '\t' || ch == '\n':
			i++
		case ch == '(' || ch == ')':
			tokens = append(tokens, filterToken{kind: string(ch), value: string(ch)})
			i++
		case strings.HasPrefix(raw[i:], ">=") || strings.HasPrefix(raw[i:], "<=") || strings.HasPrefix(raw[i:], "!="):
			tokens = append(tokens, filterToken{kind: "op", value: raw[i : i+2]})
			i += 2
		case ch == '=' || ch == '>' || ch == '<':
			tokens = append(tokens, filterToken{kind: "op", value: string(ch)})
			i++
		case ch == '\'' || ch == '"':
			quote := ch
			j := i + 1
			for j < len(raw) && raw[j] != quote {
				j++
			}
			if j >= len(raw) {
				return nil, fmt.Errorf("unterminated string")
			}
			tokens = append(tokens, filterToken{kind: "string", value: raw[i+1 : j]})
			i = j + 1
		default:
			j := i
			for j < len(raw) && !strings.ContainsRune(" ()\t\n", rune(raw[j])) {
				j++
			}
			word := raw[i:j]
			lower := strings.ToLower(word)
			switch lower {
			case "and", "or", "like":
				tokens = append(tokens, filterToken{kind: lower, value: lower})
			default:
				tokens = append(tokens, filterToken{kind: "ident", value: word})
			}
			i = j
		}
	}
	return tokens, nil
}

type filterParser struct {
	tokens []filterToken
	pos    int
}

func (p *filterParser) parse() (Predicate, error) {
	return p.parseOr()
}

func (p *filterParser) parseOr() (Predicate, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek("or") {
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l := left
		r := right
		left = PredicateFunc(func(resource *gnocchi.Resource) bool {
			return l.Match(resource) || r.Match(resource)
		})
	}
	return left, nil
}

func (p *filterParser) parseAnd() (Predicate, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.peek("and") {
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		l := left
		r := right
		left = PredicateFunc(func(resource *gnocchi.Resource) bool {
			return l.Match(resource) && r.Match(resource)
		})
	}
	return left, nil
}

func (p *filterParser) parseTerm() (Predicate, error) {
	if p.peek("(") {
		p.pos++
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.peek(")") {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return expr, nil
	}
	return p.parseComparison()
}

func (p *filterParser) parseComparison() (Predicate, error) {
	field, err := p.expect("ident")
	if err != nil {
		return nil, err
	}
	operatorToken := p.current()
	if operatorToken.kind != "op" && operatorToken.kind != "like" {
		return nil, fmt.Errorf("expected comparison operator")
	}
	p.pos++
	valueToken := p.current()
	if valueToken.kind != "ident" && valueToken.kind != "string" {
		return nil, fmt.Errorf("expected comparison value")
	}
	p.pos++
	return compileComparison(operatorToken.value, field.value, parseScalarToken(valueToken))
}

func (p *filterParser) expect(kind string) (filterToken, error) {
	token := p.current()
	if token.kind != kind {
		return filterToken{}, fmt.Errorf("expected %s", kind)
	}
	p.pos++
	return token, nil
}

func (p *filterParser) current() filterToken {
	if p.pos >= len(p.tokens) {
		return filterToken{}
	}
	return p.tokens[p.pos]
}

func (p *filterParser) peek(kind string) bool {
	return p.current().kind == kind
}

func parseScalarToken(token filterToken) any {
	if token.kind == "string" {
		return token.value
	}
	if value, err := strconv.ParseFloat(token.value, 64); err == nil {
		return value
	}
	return token.value
}
