package contribution

import (
	"errors"
	"testing"
)

func TestMetricCatalogBindsEachNameToOneUnit(t *testing.T) {
	seen := map[MetricName]bool{}
	for _, def := range metricCatalog {
		if seen[def.Name] {
			t.Errorf("metric %q is declared more than once", def.Name)
		}
		seen[def.Name] = true
		if !def.Unit.valid() {
			t.Errorf("metric %q declares unknown unit %q", def.Name, def.Unit)
		}
		if def.Measures == "" {
			t.Errorf("metric %q has no description of what it measures", def.Name)
		}
		got, err := LookupMetric(def.Name)
		if err != nil || got != def {
			t.Errorf("LookupMetric(%q) = %+v, %v; want the catalog entry", def.Name, got, err)
		}
	}
}

func TestLookupMetricRejectsNamesOutsideTheCatalog(t *testing.T) {
	tests := []struct {
		name    MetricName
		wantErr error
	}{
		{"", ErrMissingField},
		{"gpu-time", ErrUnknownMetric},
		{"lease-wall-time+gpu-active-time", ErrUnknownMetric},
		{"Lease-Wall-Time", ErrUnknownMetric},
		{"lease-wall-time ", ErrUnknownMetric},
	}
	for _, tt := range tests {
		if _, err := LookupMetric(tt.name); !errors.Is(err, tt.wantErr) {
			t.Errorf("LookupMetric(%q) error = %v, want %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestQuantityMustUseTheUnitDeclaredByItsMetric(t *testing.T) {
	tests := []struct {
		name    string
		metric  MetricName
		unit    Unit
		wantErr error
	}{
		{"lease wall time in milliseconds", MetricLeaseWallTime, UnitMillisecond, nil},
		{"lease wall time in bytes", MetricLeaseWallTime, UnitByte, ErrUnitMismatch},
		{"GPU active time in milliseconds", MetricGPUActiveTime, UnitMillisecond, nil},
		{"GPU active time in bytes", MetricGPUActiveTime, UnitByte, ErrUnitMismatch},
		{"network transfer in bytes", MetricNetworkTransferBytes, UnitByte, nil},
		{"network transfer in milliseconds", MetricNetworkTransferBytes, UnitMillisecond, ErrUnitMismatch},
		{"unit outside the closed set", MetricLeaseWallTime, "s", ErrUnknownUnit},
		{"GPU-hour is not a unit", MetricGPUActiveTime, "GPU-hour", ErrUnknownUnit},
		{"empty unit", MetricLeaseWallTime, "", ErrMissingField},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := LookupMetric(tt.metric)
			if err != nil {
				t.Fatalf("LookupMetric() error = %v", err)
			}
			err = checkQuantity(def, Quantity{Unit: tt.unit, Value: 1000})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("checkQuantity() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestLeaseWallTimeAndGPUActiveTimeAreSeparateMetrics checks that the two
// millisecond metrics cannot be read as one another. Both use milliseconds, so
// only the metric name separates them.
func TestLeaseWallTimeAndGPUActiveTimeAreSeparateMetrics(t *testing.T) {
	lease, err := LookupMetric(MetricLeaseWallTime)
	if err != nil {
		t.Fatalf("LookupMetric(lease) error = %v", err)
	}
	gpu, err := LookupMetric(MetricGPUActiveTime)
	if err != nil {
		t.Fatalf("LookupMetric(gpu) error = %v", err)
	}
	if lease.Name == gpu.Name {
		t.Fatalf("lease and GPU metrics share one name %q", lease.Name)
	}
	if lease.Measures == gpu.Measures {
		t.Fatalf("lease and GPU metrics share one meaning")
	}

	in := testInput(t)
	in.Metric = MetricLeaseWallTime
	leaseRecord := mustRecord(t, in)
	if leaseRecord.Metric() != MetricLeaseWallTime {
		t.Errorf("lease record Metric() = %q", leaseRecord.Metric())
	}

	in.Metric = MetricGPUActiveTime
	gpuRecord := mustRecord(t, in)
	if gpuRecord.Metric() != MetricGPUActiveTime {
		t.Errorf("GPU record Metric() = %q", gpuRecord.Metric())
	}

	if leaseDigest, gpuDigest := mustDigest(t, leaseRecord), mustDigest(t, gpuRecord); leaseDigest == gpuDigest {
		t.Errorf("records under different metric names share a digest")
	}
}
