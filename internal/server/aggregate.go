package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/gnocchi"
)

type expression struct {
	Number *float64
	String *string
	List   []expression
}

type valueKind string

const (
	valueKindScalar valueKind = "scalar"
	valueKindSeries valueKind = "series"
	valueKindSet    valueKind = "set"
)

type aggregateValue struct {
	Kind   valueKind
	Scalar float64
	Series map[int64]float64
	Set    []map[int64]float64
}

func parseAggregateOperations(raw any) (expression, error) {
	switch value := raw.(type) {
	case string:
		return parseSExpr(value)
	default:
		return expressionFromAny(value)
	}
}

func expressionFromAny(value any) (expression, error) {
	switch typed := value.(type) {
	case float64:
		return expression{Number: &typed}, nil
	case string:
		return expression{String: &typed}, nil
	case []any:
		items := make([]expression, 0, len(typed))
		for _, item := range typed {
			expr, err := expressionFromAny(item)
			if err != nil {
				return expression{}, err
			}
			items = append(items, expr)
		}
		return expression{List: items}, nil
	default:
		return expression{}, fmt.Errorf("unsupported operations node %T", value)
	}
}

type sexprParser struct {
	tokens []string
	pos    int
}

func parseSExpr(raw string) (expression, error) {
	tokens := tokenizeSExpr(raw)
	parser := &sexprParser{tokens: tokens}
	return parser.parse()
}

func tokenizeSExpr(raw string) []string {
	replacer := strings.NewReplacer("(", " ( ", ")", " ) ")
	return strings.Fields(replacer.Replace(raw))
}

func (p *sexprParser) parse() (expression, error) {
	if p.pos >= len(p.tokens) {
		return expression{}, fmt.Errorf("unexpected end of operations")
	}
	token := p.tokens[p.pos]
	p.pos++
	switch token {
	case "(":
		items := []expression{}
		for p.pos < len(p.tokens) && p.tokens[p.pos] != ")" {
			expr, err := p.parse()
			if err != nil {
				return expression{}, err
			}
			items = append(items, expr)
		}
		if p.pos >= len(p.tokens) || p.tokens[p.pos] != ")" {
			return expression{}, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return expression{List: items}, nil
	default:
		if number, err := strconv.ParseFloat(token, 64); err == nil {
			return expression{Number: &number}, nil
		}
		return expression{String: &token}, nil
	}
}

func (s *Server) evaluateAggregate(ctx context.Context, resourceType string, resources []*gnocchi.Resource, expr expression, params measureParams) (aggregateValue, error) {
	if expr.Number != nil {
		return aggregateValue{Kind: valueKindScalar, Scalar: *expr.Number}, nil
	}
	if expr.String != nil {
		return aggregateValue{}, fmt.Errorf("unexpected bare identifier %q", *expr.String)
	}
	if len(expr.List) == 0 {
		return aggregateValue{}, fmt.Errorf("empty operations list")
	}
	if expr.List[0].String == nil {
		return aggregateValue{}, fmt.Errorf("operation must start with an identifier")
	}

	op := strings.ToLower(*expr.List[0].String)
	switch op {
	case "metric":
		if len(expr.List) < 2 || expr.List[1].String == nil {
			return aggregateValue{}, fmt.Errorf("metric operation requires a metric name")
		}
		metricName := *expr.List[1].String
		aggregation := params.Aggregation
		if len(expr.List) >= 3 && expr.List[2].String != nil {
			aggregation = strings.ToLower(*expr.List[2].String)
		}
		set := make([]map[int64]float64, 0, len(resources))
		for _, resource := range resources {
			measures, err := s.queryMeasuresWithParams(ctx, resourceType, resource.ID, metricName, aggregation, params)
			if err != nil && !errors.Is(err, gnocchi.ErrNotFound) {
				return aggregateValue{}, err
			}
			series := map[int64]float64{}
			for _, measure := range measures {
				series[measure.Timestamp.Unix()] = measure.Value
			}
			if len(series) > 0 {
				set = append(set, series)
			}
		}
		return aggregateValue{Kind: valueKindSet, Set: set}, nil
	case "aggregate":
		if len(expr.List) != 3 || expr.List[1].String == nil {
			return aggregateValue{}, fmt.Errorf("aggregate operation requires a method and one expression")
		}
		method := strings.ToLower(*expr.List[1].String)
		value, err := s.evaluateAggregate(ctx, resourceType, resources, expr.List[2], params)
		if err != nil {
			return aggregateValue{}, err
		}
		if value.Kind != valueKindSet {
			return aggregateValue{}, fmt.Errorf("aggregate expects a metric set")
		}
		return aggregateValue{Kind: valueKindSeries, Series: aggregateSeriesSet(method, value.Set)}, nil
	case "+", "-", "*", "/":
		if len(expr.List) < 3 {
			return aggregateValue{}, fmt.Errorf("%s requires at least two operands", op)
		}
		left, err := s.evaluateAggregate(ctx, resourceType, resources, expr.List[1], params)
		if err != nil {
			return aggregateValue{}, err
		}
		for _, arg := range expr.List[2:] {
			right, err := s.evaluateAggregate(ctx, resourceType, resources, arg, params)
			if err != nil {
				return aggregateValue{}, err
			}
			left, err = applyBinaryOperation(op, left, right)
			if err != nil {
				return aggregateValue{}, err
			}
		}
		return left, nil
	default:
		return aggregateValue{}, fmt.Errorf("unsupported aggregate operation %q", op)
	}
}

func aggregateSeriesSet(method string, set []map[int64]float64) map[int64]float64 {
	buckets := map[int64][]float64{}
	for _, series := range set {
		for ts, value := range series {
			buckets[ts] = append(buckets[ts], value)
		}
	}
	out := map[int64]float64{}
	for ts, values := range buckets {
		out[ts] = collapse(method, values)
	}
	return out
}

func collapse(method string, values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	switch method {
	case "sum":
		sum := 0.0
		for _, value := range values {
			sum += value
		}
		return sum
	case "min":
		min := values[0]
		for _, value := range values[1:] {
			if value < min {
				min = value
			}
		}
		return min
	case "max":
		max := values[0]
		for _, value := range values[1:] {
			if value > max {
				max = value
			}
		}
		return max
	case "count":
		return float64(len(values))
	case "last":
		return values[len(values)-1]
	default:
		sum := 0.0
		for _, value := range values {
			sum += value
		}
		return sum / float64(len(values))
	}
}

func applyBinaryOperation(op string, left, right aggregateValue) (aggregateValue, error) {
	if left.Kind == valueKindSet || right.Kind == valueKindSet {
		return aggregateValue{}, fmt.Errorf("arithmetic operations require scalar or series operands")
	}
	switch {
	case left.Kind == valueKindScalar && right.Kind == valueKindScalar:
		return aggregateValue{Kind: valueKindScalar, Scalar: operate(op, left.Scalar, right.Scalar)}, nil
	case left.Kind == valueKindSeries && right.Kind == valueKindScalar:
		return aggregateValue{Kind: valueKindSeries, Series: applySeriesScalar(op, left.Series, right.Scalar, false)}, nil
	case left.Kind == valueKindScalar && right.Kind == valueKindSeries:
		return aggregateValue{Kind: valueKindSeries, Series: applySeriesScalar(op, right.Series, left.Scalar, true)}, nil
	case left.Kind == valueKindSeries && right.Kind == valueKindSeries:
		return aggregateValue{Kind: valueKindSeries, Series: applySeriesSeries(op, left.Series, right.Series)}, nil
	default:
		return aggregateValue{}, fmt.Errorf("unsupported arithmetic operands")
	}
}

func applySeriesScalar(op string, series map[int64]float64, scalar float64, scalarFirst bool) map[int64]float64 {
	out := map[int64]float64{}
	for ts, value := range series {
		if scalarFirst {
			out[ts] = operate(op, scalar, value)
		} else {
			out[ts] = operate(op, value, scalar)
		}
	}
	return out
}

func applySeriesSeries(op string, left, right map[int64]float64) map[int64]float64 {
	out := map[int64]float64{}
	for ts, leftValue := range left {
		if rightValue, ok := right[ts]; ok {
			out[ts] = operate(op, leftValue, rightValue)
		}
	}
	return out
}

func operate(op string, left, right float64) float64 {
	switch op {
	case "+":
		return left + right
	case "-":
		return left - right
	case "*":
		return left * right
	case "/":
		if right == 0 {
			return 0
		}
		return left / right
	default:
		return 0
	}
}
