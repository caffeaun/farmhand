package main

import (
	"errors"
	"testing"
)

func TestLaunchCmd_HappyPath(t *testing.T) {
	fake := withFakeManager(t)

	if err := launchCmd.Flags().Set("device", "dev-1"); err != nil {
		t.Fatal(err)
	}
	if err := launchCmd.Flags().Set("package", "com.example.app"); err != nil {
		t.Fatal(err)
	}

	if err := runLaunch(launchCmd, nil); err != nil {
		t.Fatalf("runLaunch: %v", err)
	}
	want := fakeLaunchCall{"dev-1", "com.example.app"}
	if fake.launch == nil || *fake.launch != want {
		t.Errorf("launch = %v, want %v", fake.launch, want)
	}
}

func TestLaunchCmd_PropagatesManagerError(t *testing.T) {
	fake := withFakeManager(t)
	fake.err = errors.New("invalid package id \"NotAPkg\": must match ...")

	_ = launchCmd.Flags().Set("device", "dev-1")
	_ = launchCmd.Flags().Set("package", "NotAPkg")

	err := runLaunch(launchCmd, nil)
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
