package core

import (
	"errors"
	"testing"
)

func TestNewExecutor(t *testing.T) {
	// Test True
	exec := NewExecutor(true)
	if !exec.DryRun {
		t.Errorf("Expected DryRun to be true")
	}

	// Test False
	execFalse := NewExecutor(false)
	if execFalse.DryRun {
		t.Errorf("Expected DryRun to be false")
	}
}

func TestExecutor_Run_DryRun(t *testing.T) {
	exec := NewExecutor(true)
	wasCalled := false

	// This function should NEVER execute because DryRun is true
	err := exec.Run("dangerous action", func() error {
		wasCalled = true
		return errors.New("this should not happen")
	}, "target", "production")

	if err != nil {
		t.Errorf("Expected no error during DryRun, got: %v", err)
	}
	if wasCalled {
		t.Errorf("CRITICAL: Executed function while DryRun was enabled!")
	}
}

func TestExecutor_Run_Success(t *testing.T) {
	exec := NewExecutor(false)
	wasCalled := false

	err := exec.Run("safe action", func() error {
		wasCalled = true
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if !wasCalled {
		t.Errorf("Expected function to be executed, but it wasn't")
	}
}

func TestExecutor_Run_Failure(t *testing.T) {
	exec := NewExecutor(false)
	wasCalled := false
	expectedErr := errors.New("simulated network failure")

	err := exec.Run("failing action", func() error {
		wasCalled = true
		return expectedErr
	})

	// Ensure the exact error is passed back up the chain
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}
	if !wasCalled {
		t.Errorf("Expected function to be executed, but it wasn't")
	}
}
