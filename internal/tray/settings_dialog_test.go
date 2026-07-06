package tray

import (
	"testing"
)

// TestGwlStyleConstant verifies that gwlStyle is -16 (GWL_STYLE).
// A previous bug had it as -15 (GWL_EXSTYLE) due to an incorrect
// two's complement calculation. This would cause GetWindowLong/SetWindowLong
// to read/write the wrong attribute, corrupting controls.
func TestGwlStyleConstant(t *testing.T) {
	// GWL_STYLE is -16. In uintptr (unsigned), -16 = ^uintptr(15).
	// The bug was ^uintptr(15) + 1 which = -15 (GWL_EXSTYLE).
	// Verify by checking the low bits match -16 pattern.
	u := uintptr(gwlStyle)
	// -16 in two's complement: all 1s except bits 0-3 are 0.
	if u&0xF != 0 {
		t.Errorf("gwlStyle low nibble = 0x%X, want 0 (GWL_STYLE=-16 has low nibble 0)", u&0xF)
	}
	// Verify it's NOT -15 (which would have low nibble 1).
	if u&0xF == 1 {
		t.Error("gwlStyle appears to be -15 (GWL_EXSTYLE), should be -16 (GWL_STYLE)")
	}
	// Cross-check: -16 XOR -15 should differ only in bit 0.
	neg16 := ^uintptr(15)    // 0xFFF...F0
	if u != neg16 {
		t.Errorf("gwlStyle = 0x%X, want 0x%X", u, neg16)
	}
}

// TestMessageLoopProcs verifies that the critical Win32 procs needed for
// interactive child controls are loaded (non-nil). Without TranslateMessage
// and IsDialogMessageW, edit controls can't process typed characters and
// radio buttons can't be navigated with arrow keys.
func TestMessageLoopProcs(t *testing.T) {
	if pTranslateMessage == nil {
		t.Error("pTranslateMessage is nil — edit controls won't receive WM_CHAR")
	}
	if pIsDialogMessageW == nil {
		t.Error("pIsDialogMessageW is nil — dialog navigation (tab/arrows) won't work")
	}
}

// TestWindowStyleConstants verifies Win32 style constants have correct values.
// Duplicate or incorrect values would cause controls to be non-functional.
func TestWindowStyleConstants(t *testing.T) {
	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"wsChild", wsChild, 0x40000000},
		{"wsVisible", wsVisible, 0x10000000},
		{"wsCaption", wsCaption, 0x00C00000},
		{"wsSysMenu", wsSysMenu, 0x00080000},
		{"wsThickframe", wsThickframe, 0x00040000},
		{"wsTabstop", wsTabstop, 0x00010000},
		{"wsGroup", wsGroup, 0x00020000},
		{"wsDisabled", wsDisabled, 0x08000000},
		{"wsBorder", wsBorder, 0x00800000},
		{"wsVscroll", wsVscroll, 0x00200000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = 0x%08X, want 0x%08X", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestButtonStyleConstants verifies BS_* constants.
func TestButtonStyleConstants(t *testing.T) {
	if bsAutocheckbox != 0x0003 {
		t.Errorf("bsAutocheckbox = 0x%X, want 0x0003", bsAutocheckbox)
	}
	if bsAutoradioButton != 0x0009 {
		t.Errorf("bsAutoradioButton = 0x%X, want 0x0009", bsAutoradioButton)
	}
	if bsCenter != 0x0300 {
		t.Errorf("bsCenter = 0x%X, want 0x0300", bsCenter)
	}
}

// TestMessageConstants verifies WM_* message IDs.
func TestMessageConstants(t *testing.T) {
	if wmCommand != 0x0111 {
		t.Errorf("wmCommand = 0x%X, want 0x0111", wmCommand)
	}
	if wmDestroy != 0x0002 {
		t.Errorf("wmDestroy = 0x%X, want 0x0002", wmDestroy)
	}
	if wmSize != 0x0005 {
		t.Errorf("wmSize = 0x%X, want 0x0005", wmSize)
	}
	if wmSetFont != 0x0030 {
		t.Errorf("wmSetFont = 0x%X, want 0x0030", wmSetFont)
	}
}

// TestSignedConversion verifies that the GWL constant is correct.
func TestSignedConversion(t *testing.T) {
	// -16 in two's complement: all bits set except lower 4.
	neg16 := ^uintptr(15)
	if uintptr(gwlStyle) != neg16 {
		t.Errorf("gwlStyle = 0x%X, want 0x%X (-16)", uintptr(gwlStyle), neg16)
	}
}
