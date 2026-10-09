package telemetry

import (
	"errors"
	"reflect"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var errMetricAggregation = errors.New("unsupported metric aggregation")

type metricPart struct {
	data         metricdata.ResourceMetrics
	acknowledged bool
}

// Attribute sets, Resources and time values are immutable. Mutable exported
// slice containers (including histogram buckets and exemplar attributes) are
// copied recursively, after the exhaustive aggregation allowlist below.
func cloneMetricValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneMetricValue(v.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" {
				out.Field(i).Set(cloneMetricValue(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}
func metricAggregationValue(data metricdata.Aggregation) (reflect.Value, error) {
	switch data.(type) {
	case metricdata.Gauge[int64], metricdata.Gauge[float64], metricdata.Sum[int64], metricdata.Sum[float64], metricdata.Histogram[int64], metricdata.Histogram[float64], metricdata.ExponentialHistogram[int64], metricdata.ExponentialHistogram[float64], metricdata.Summary:
		return reflect.ValueOf(data), nil
	default:
		return reflect.Value{}, errMetricAggregation
	}
}
func cloneResourceMetrics(rm *metricdata.ResourceMetrics) (metricdata.ResourceMetrics, error) {
	out := metricdata.ResourceMetrics{Resource: rm.Resource, ScopeMetrics: make([]metricdata.ScopeMetrics, len(rm.ScopeMetrics))}
	for i, scope := range rm.ScopeMetrics {
		out.ScopeMetrics[i] = metricdata.ScopeMetrics{Scope: scope.Scope, Metrics: make([]metricdata.Metrics, len(scope.Metrics))}
		for j, metric := range scope.Metrics {
			v, err := metricAggregationValue(metric.Data)
			if err != nil {
				return metricdata.ResourceMetrics{}, err
			}
			metric.Data = cloneMetricValue(v).Interface().(metricdata.Aggregation)
			out.ScopeMetrics[i].Metrics[j] = metric
		}
	}
	return out, nil
}
func freezeMetricParts(rm *metricdata.ResourceMetrics, batch int) ([]metricPart, error) {
	frozen, err := cloneResourceMetrics(rm)
	if err != nil {
		return nil, err
	}
	if batch <= 0 {
		return []metricPart{{data: frozen}}, nil
	}
	var result []metricPart
	current := metricdata.ResourceMetrics{Resource: frozen.Resource}
	count := 0
	finish := func() {
		if count > 0 {
			result = append(result, metricPart{data: current})
			current = metricdata.ResourceMetrics{Resource: frozen.Resource}
			count = 0
		}
	}
	for _, scope := range frozen.ScopeMetrics {
		for _, metric := range scope.Metrics {
			data, _ := metricAggregationValue(metric.Data)
			points := data.FieldByName("DataPoints")
			for first := 0; first < points.Len(); {
				if count == batch {
					finish()
				}
				n := min(batch-count, points.Len()-first)
				piece := reflect.New(data.Type()).Elem()
				piece.Set(data)
				piece.FieldByName("DataPoints").Set(points.Slice(first, first+n))
				m := metric
				m.Data = piece.Interface().(metricdata.Aggregation)
				// A scope wrapper per metric fragment is bounded by datapoint count. The
				// owned immutable point backing storage is shared, never duplicated.
				current.ScopeMetrics = append(current.ScopeMetrics, metricdata.ScopeMetrics{Scope: scope.Scope, Metrics: []metricdata.Metrics{m}})
				count += n
				first += n
			}
		}
	}
	finish()
	if len(result) == 0 {
		result = append(result, metricPart{data: frozen})
	}
	return result, nil
}
