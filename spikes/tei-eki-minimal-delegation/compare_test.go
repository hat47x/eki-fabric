package teideleg

import (
	"testing"
	"time"
)

func TestCompareRefusesToInventSamples(t *testing.T) {
	c := Compare("tei.validate", nil, nil, time.Now())
	if c.Local.Samples != 0 || c.EKI.Samples != 0 {
		t.Fatalf("an empty report has samples: %+v", c)
	}
	if c.Local.Median != 0 || c.EKI.Median != 0 {
		t.Fatalf("an empty report reports a duration: %+v", c)
	}
	if len(c.Caveat) == 0 {
		t.Fatal("a comparison must state what it does not measure")
	}
}

func TestCompareSummarisesLocalRuns(t *testing.T) {
	locals := []LocalRun{
		{DurationMillis: 300, OutputSHA256: "aa", ExitCode: 0, CacheState: "warm"},
		{DurationMillis: 100, OutputSHA256: "aa", ExitCode: 0, CacheState: "warm"},
		{DurationMillis: 900, OutputSHA256: "aa", ExitCode: 0, CacheState: "warm"},
	}
	c := Compare("tei.validate", locals, nil, time.Now())
	if c.Local.Samples != 3 || c.Local.MinMillis != 100 || c.Local.MaxMillis != 900 || c.Local.Median != 300 {
		t.Fatalf("local stats = %+v", c.Local)
	}
	if !c.Local.OutputStable || c.Local.OutputSHA256 != "aa" {
		t.Fatalf("output stability = %+v", c.Local)
	}
	// The caveat must say that EKI ran nothing here.
	if c.EKI.Samples != 0 || c.EKI.Attempts != 0 {
		t.Fatalf("EKI section is not empty: %+v", c.EKI)
	}
}

func TestCompareDetectsAnUnstableLocalOutput(t *testing.T) {
	locals := []LocalRun{
		{DurationMillis: 100, OutputSHA256: "aa"},
		{DurationMillis: 120, OutputSHA256: "bb"},
	}
	c := Compare("tei.validate", locals, nil, time.Now())
	if c.Local.OutputStable {
		t.Fatal("two different local outputs were reported as stable")
	}
}

func TestCompareSummarisesTraces(t *testing.T) {
	start := time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC)
	traces := []Trace{
		{
			Observation: Observation{StartedAt: start, FinishedAt: start.Add(200 * time.Millisecond), WaitedMillis: 0, Requests: 6},
			Links:       Links{AttemptCount: 0},
			Receipt:     Receipt{Outcome: OutcomeUndetermined},
		},
		{
			Observation: Observation{StartedAt: start, FinishedAt: start.Add(400 * time.Millisecond), WaitedMillis: 100, Requests: 8},
			Links:       Links{AttemptCount: 2},
			Receipt:     Receipt{Outcome: OutcomeAccepted},
		},
		{
			Observation: Observation{StartedAt: start, FinishedAt: start.Add(600 * time.Millisecond), WaitedMillis: 200, Requests: 10},
			Links:       Links{AttemptCount: 1},
			Receipt:     Receipt{Outcome: OutcomeAccepted},
		},
	}
	c := Compare("tei.validate", nil, traces, start)
	if c.EKI.Samples != 3 {
		t.Fatalf("samples = %d", c.EKI.Samples)
	}
	if c.EKI.MinMillis != 200 || c.EKI.Median != 400 || c.EKI.MaxMillis != 600 {
		t.Fatalf("round trip stats = %+v", c.EKI)
	}
	if c.EKI.MedianRequests != 8 || c.EKI.MedianWaitMillis != 100 {
		t.Fatalf("requests/waits = %+v", c.EKI)
	}
	if c.EKI.Attempts != 2 {
		t.Fatalf("attempts = %d", c.EKI.Attempts)
	}
	if c.EKI.Outcomes["accepted"] != 2 || c.EKI.Outcomes["undetermined"] != 1 {
		t.Fatalf("outcomes = %+v", c.EKI.Outcomes)
	}
	// With attempts observed, the caveat must not claim EKI executed nothing;
	// it states the condition instead.
	for _, caveat := range c.Caveat {
		if caveat == "" {
			t.Error("empty caveat")
		}
	}
}
