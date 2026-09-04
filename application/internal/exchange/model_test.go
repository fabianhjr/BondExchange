package exchange

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestIdentifierParsing(t *testing.T) {
	t.Parallel()

	if user, err := ParseUserID(testUserID); err != nil || user != testUserID {
		t.Fatalf("ParseUserID() = %q, %v", user, err)
	}
	if _, err := ParseUserID(""); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("ParseUserID(empty) error = %v", err)
	}
	if offer, err := ParseOfferID(testOfferID); err != nil || offer != testOfferID {
		t.Fatalf("ParseOfferID() = %q, %v", offer, err)
	}
	if _, err := ParseOfferID(""); !errors.Is(err, ErrInvalidOfferID) {
		t.Fatalf("ParseOfferID(empty) error = %v", err)
	}
	for _, invalid := range []string{strings.Repeat("x", 129), "user\nname", "41db1265-8bc1-4ab3-992f-885799a4af1d"} {
		if _, err := ParseUserID(invalid); !errors.Is(err, ErrInvalidUserID) {
			t.Fatalf("ParseUserID(%q) error = %v", invalid, err)
		}
	}
}

func TestParseBondSeries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		expected  BondSeries
		wantError bool
	}{
		{name: "canonical", input: "BND2026", expected: "BND2026"},
		{name: "canonicalizes lowercase", input: "bNd123", expected: "BND123"},
		{name: "minimum length", input: "A01", expected: "A01"},
		{name: "maximum length", input: strings.Repeat("Z", 40), expected: BondSeries(strings.Repeat("Z", 40))},
		{name: "too short", input: "A1", wantError: true},
		{name: "too long", input: strings.Repeat("A", 41), wantError: true},
		{name: "punctuation", input: "BND-1", wantError: true},
		{name: "non ASCII", input: "BÑD", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := ParseBondSeries(test.input)
			if test.wantError {
				if !errors.Is(err, ErrInvalidBondSeries) {
					t.Fatalf("ParseBondSeries(%q) error = %v", test.input, err)
				}
				return
			}
			if err != nil || actual != test.expected {
				t.Fatalf("ParseBondSeries(%q) = %q, %v; want %q", test.input, actual, err, test.expected)
			}
		})
	}
}

func TestPriceAndCurrencyParsing(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"0.0001", "100.25", "100.25000", "9999999999.9999"} {
		price, err := ParsePrice(valid)
		if err != nil || !price.Equal(decimal.RequireFromString(valid)) {
			t.Fatalf("ParsePrice(%q) = %s, %v", valid, price, err)
		}
	}
	for _, invalid := range []string{
		"-1",
		"0",
		"0.00001",
		"1.23456",
		"10000000000",
		"not-a-decimal",
	} {
		if _, err := ParsePrice(invalid); !errors.Is(err, ErrInvalidPrice) {
			t.Fatalf("ParsePrice(%q) error = %v", invalid, err)
		}
	}
	if code, err := ParseCurrencyCode("USD"); err != nil || code != "USD" {
		t.Fatalf("ParseCurrencyCode() = %q, %v", code, err)
	}
	if _, err := ParseCurrencyCode(""); !errors.Is(err, ErrInvalidCurrencyCode) {
		t.Fatalf("ParseCurrencyCode(empty) error = %v", err)
	}
	for _, invalid := range []string{"usd", "US1", "USÉ"} {
		if _, err := ParseCurrencyCode(invalid); !errors.Is(err, ErrInvalidCurrencyCode) {
			t.Fatalf("ParseCurrencyCode(%q) error = %v", invalid, err)
		}
	}
}

func TestIdempotencyKeyValidation(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{testNonce} {
		if !IsValidIdempotencyKey(valid) {
			t.Fatalf("valid key %q was rejected", valid)
		}
	}
	for _, invalid := range []string{"short", strings.Repeat("x", 129), testOfferID, strings.ToUpper(testNonce)} {
		if IsValidIdempotencyKey(invalid) {
			t.Fatalf("invalid key %q was accepted", invalid)
		}
	}
}

func FuzzParseBondSeries(fuzzer *testing.F) {
	for _, seed := range []string{"BND", "bond2026", "A1", "BND-1", "BÑD"} {
		fuzzer.Add(seed)
	}
	fuzzer.Fuzz(func(t *testing.T, input string) {
		series, err := ParseBondSeries(input)
		if err != nil {
			return
		}
		if !IsCanonicalBondSeries(string(series)) {
			t.Fatalf("accepted noncanonical series %q from %q", series, input)
		}
		if string(series) != strings.ToUpper(input) {
			t.Fatalf("series %q is not uppercase form of %q", series, input)
		}
	})
}
