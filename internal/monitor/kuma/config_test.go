package kuma

import (
	"reflect"
	"testing"
)

func TestGetEnvHelpers(t *testing.T) {
	t.Setenv("TEST_STRING", "hello_world")
	t.Setenv("TEST_INT", "42")
	t.Setenv("TEST_SLICE", "tag1, tag2 , tag3")

	// Test Strings
	if val := getEnvString("TEST_STRING", "fallback"); val != "hello_world" {
		t.Errorf("Expected 'hello_world', got '%s'", val)
	}
	if val := getEnvString("MISSING_VAR", "fallback"); val != "fallback" {
		t.Errorf("Expected 'fallback', got '%s'", val)
	}

	// Test Integers
	if val := getEnvInt("TEST_INT", 10); val != 42 {
		t.Errorf("Expected 42, got %d", val)
	}
	if val := getEnvInt64("MISSING_VAR", 99); val != 99 {
		t.Errorf("Expected fallback 99, got %d", val)
	}
	if val := getEnvInt("TEST_STRING", 10); val != 10 { // Invalid int should fallback
		t.Errorf("Expected fallback 10 on invalid parse, got %d", val)
	}

	// Test String Slices (Should automatically trim spaces)
	expectedSlice := []string{"tag1", "tag2", "tag3"}
	actualSlice := getEnvStringSlice("TEST_SLICE", []string{"default"})
	if !reflect.DeepEqual(actualSlice, expectedSlice) {
		t.Errorf("Expected %v, got %v", expectedSlice, actualSlice)
	}
}
