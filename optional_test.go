package main

import "testing"

func Test_BasicOptional(t *testing.T) {
	op := NewOpValue("Flavio")

	if !op.HasValue() {
		t.Error("Flavio should be VALID here!")
	}

	op = NewOpEmpty[string]()

	if op.HasValue() {
		t.Error("Op should be EMPTY here!")
	}

}
