package resourceprovider

import (
	"errors"
	"testing"
)

func TestRetrySensitive(t *testing.T) {
	cases := []struct {
		name string
		task TaskProfile
		want bool
	}{
		{name: "pure and idempotent", task: TaskProfile{IdempotencyBasis: true}, want: false},
		{name: "side effect with idempotency basis", task: TaskProfile{SideEffect: true, IdempotencyBasis: true}, want: true},
		{name: "side effect without basis", task: TaskProfile{SideEffect: true}, want: true},
		{name: "no side effect but no basis", task: TaskProfile{}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.task.RetrySensitive(); got != tc.want {
				t.Fatalf("RetrySensitive() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlaceWithRetryRule covers rule (a).
func TestPlaceWithRetryRule(t *testing.T) {
	sideEffect := TaskProfile{SideEffect: true}
	noBasis := TaskProfile{}
	idempotent := TaskProfile{IdempotencyBasis: true}

	onDisableable := validDeclaration()
	onDisableable.Retry = RetryDeclaration{OnByDefault: true, DisableablePerSubmission: true}

	onLocked := validDeclaration()
	onLocked.Retry = RetryDeclaration{OnByDefault: true, DisableablePerSubmission: false}

	offLocked := validDeclaration()
	offLocked.Retry = RetryDeclaration{OnByDefault: false, DisableablePerSubmission: false}

	cases := []struct {
		name    string
		task    TaskProfile
		decl    Declaration
		want    RetrySetting
		wantErr error
	}{
		{name: "side effect on disableable provider is disabled", task: sideEffect, decl: onDisableable, want: RetryDisabled},
		{name: "no basis on disableable provider is disabled", task: noBasis, decl: onDisableable, want: RetryDisabled},
		{name: "side effect on locked provider is refused", task: sideEffect, decl: onLocked, wantErr: ErrRetryNotDisableable},
		{name: "side effect on off-by-default locked provider is still refused", task: sideEffect, decl: offLocked, wantErr: ErrRetryNotDisableable},
		{name: "no basis on locked provider is refused", task: noBasis, decl: onLocked, wantErr: ErrRetryNotDisableable},
		{name: "idempotent task keeps provider default on locked provider", task: idempotent, decl: onLocked, want: RetryProviderDefault},
		{name: "idempotent task keeps provider default on disableable provider", task: idempotent, decl: onDisableable, want: RetryProviderDefault},
		{name: "empty provider is refused as a declaration", task: idempotent, decl: Declaration{}, wantErr: ErrInvalidDeclaration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PlaceWithRetryRule(tc.task, tc.decl)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PlaceWithRetryRule() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("PlaceWithRetryRule() error = %v, want nil", err)
			}
			if got != tc.want {
				t.Fatalf("PlaceWithRetryRule() = %q, want %q", got, tc.want)
			}
		})
	}
}
