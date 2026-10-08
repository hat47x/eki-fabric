package contribution

import "fmt"

// MaxQuantity is the largest quantity a single observation may carry, in the
// unit of its metric. Larger values are rejected, not clamped.
const MaxQuantity int64 = 1 << 40

// Unit is a closed set of measurement units. Quantities are never converted
// from one unit to another.
type Unit string

const (
	UnitMillisecond Unit = "ms"
	UnitByte        Unit = "byte"
)

func (u Unit) valid() bool {
	return u == UnitMillisecond || u == UnitByte
}

// MetricName names what was measured. Each name is bound to exactly one unit
// and one meaning in the closed catalog below, so a single name cannot stand
// for two different measurements, such as lease wall time and GPU active time,
// even when both are expressed in milliseconds.
type MetricName string

const (
	MetricLeaseWallTime        MetricName = "lease-wall-time"
	MetricGPUActiveTime        MetricName = "gpu-active-time"
	MetricNetworkTransferBytes MetricName = "network-transfer-bytes"
)

// MetricDefinition is one entry of the metric catalog.
type MetricDefinition struct {
	Name     MetricName
	Unit     Unit
	Measures string
}

var metricCatalog = [...]MetricDefinition{
	{
		Name:     MetricLeaseWallTime,
		Unit:     UnitMillisecond,
		Measures: "wall-clock time a lease was held, whether or not any device was active",
	},
	{
		Name:     MetricGPUActiveTime,
		Unit:     UnitMillisecond,
		Measures: "time a GPU was active on the work; not the time a lease was held",
	},
	{
		Name:     MetricNetworkTransferBytes,
		Unit:     UnitByte,
		Measures: "bytes transferred on behalf of the work",
	},
}

// LookupMetric returns the catalog entry for name. Names outside the catalog
// are rejected, so no free-form metric can reach a record.
func LookupMetric(name MetricName) (MetricDefinition, error) {
	if name == "" {
		return MetricDefinition{}, fmt.Errorf("%w: metric name", ErrMissingField)
	}
	for _, def := range metricCatalog {
		if def.Name == name {
			return def, nil
		}
	}
	return MetricDefinition{}, ErrUnknownMetric
}

// checkQuantity enforces the unit, sign, zero, and size rules for one quantity
// measured under def.
func checkQuantity(def MetricDefinition, q Quantity) error {
	if q.Unit == "" {
		return fmt.Errorf("%w: quantity unit", ErrMissingField)
	}
	if !q.Unit.valid() {
		return ErrUnknownUnit
	}
	if q.Unit != def.Unit {
		return ErrUnitMismatch
	}
	switch {
	case q.Value < 0:
		return ErrNegativeQuantity
	case q.Value == 0:
		return ErrZeroQuantity
	case q.Value > MaxQuantity:
		return ErrQuantityTooLarge
	}
	return nil
}
