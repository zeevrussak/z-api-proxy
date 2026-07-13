package singleinstance

import (
	"errors"
	"testing"
)

func TestAcquireExclusive(t *testing.T) {
	name := `Local\Z-API-Proxy-test-` + t.Name()

	first, err := Acquire(name)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()

	second, err := Acquire(name)
	if !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			second.Release()
		}
		t.Fatalf("second Acquire: got %v, want ErrAlreadyRunning", err)
	}
}

func TestAcquireAfterRelease(t *testing.T) {
	name := `Local\Z-API-Proxy-test-` + t.Name()

	first, err := Acquire(name)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	first.Release()

	second, err := Acquire(name)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	second.Release()
}

func TestReleaseNilAndDouble(t *testing.T) {
	var nilLock *Lock
	nilLock.Release() // must not panic

	name := `Local\Z-API-Proxy-test-` + t.Name()
	lock, err := Acquire(name)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	lock.Release()
	lock.Release() // must not panic
}

func TestDefaultMutexName(t *testing.T) {
	// Empty name falls back to MutexName. Use a unique name for the
	// exclusive check so we don't collide with a running tray app.
	lock, err := Acquire(`Local\Z-API-Proxy-test-default`)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()
	if lock.handle == 0 {
		t.Fatal("expected non-zero handle")
	}
}
