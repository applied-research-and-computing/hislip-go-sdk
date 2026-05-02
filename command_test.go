package hislip

import "testing"

func TestSCPICommand_Query(t *testing.T) {
	cmd := newSCPICommand("*IDN?", "*IDN?")
	if !cmd.Query {
		t.Error("expected Query=true for *IDN?")
	}
	cmd2 := newSCPICommand("*RST", "*RST")
	if cmd2.Query {
		t.Error("expected Query=false for *RST")
	}
}

func TestSCPICommand_Args(t *testing.T) {
	cmd := newSCPICommand("CONF:VOLT:DC 10,0.001", "CONF:VOLT:DC")
	if len(cmd.Args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(cmd.Args))
	}
	if cmd.Args[0] != "10" {
		t.Errorf("Args[0] = %q, want %q", cmd.Args[0], "10")
	}
	if cmd.Args[1] != "0.001" {
		t.Errorf("Args[1] = %q, want %q", cmd.Args[1], "0.001")
	}
}

func TestSCPICommand_NoArgs(t *testing.T) {
	cmd := newSCPICommand("*RST", "*RST")
	if len(cmd.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(cmd.Args))
	}
}

func TestSCPICommand_ArgFloat(t *testing.T) {
	cmd := newSCPICommand("CONF:VOLT:DC 10,0.001", "CONF:VOLT:DC")
	if got := cmd.ArgFloat(0, 0); got != 10.0 {
		t.Errorf("ArgFloat(0) = %v, want 10.0", got)
	}
	if got := cmd.ArgFloat(1, 0); got != 0.001 {
		t.Errorf("ArgFloat(1) = %v, want 0.001", got)
	}
	if got := cmd.ArgFloat(9, -1); got != -1 {
		t.Errorf("ArgFloat(9, -1) = %v, want -1 (default)", got)
	}
}

func TestSCPICommand_ArgInt(t *testing.T) {
	cmd := newSCPICommand("ESE 32", "ESE")
	if got := cmd.ArgInt(0, -1); got != 32 {
		t.Errorf("ArgInt(0) = %v, want 32", got)
	}
	if got := cmd.ArgInt(5, 99); got != 99 {
		t.Errorf("ArgInt(5, 99) = %v, want 99 (default)", got)
	}
}

func TestSCPICommand_ArgIntFromFloat(t *testing.T) {
	// Python's arg(0, int) truncates floats
	cmd := newSCPICommand("BURS:NCYC 3.0", "BURS:NCYC")
	if got := cmd.ArgInt(0, -1); got != 3 {
		t.Errorf("ArgInt from float 3.0 = %v, want 3", got)
	}
}

func TestSCPICommand_ArgString(t *testing.T) {
	cmd := newSCPICommand("CONF:VOLT AUTO", "CONF:VOLT")
	if got := cmd.ArgString(0, ""); got != "AUTO" {
		t.Errorf("ArgString(0) = %q, want %q", got, "AUTO")
	}
	if got := cmd.ArgString(5, "DEF"); got != "DEF" {
		t.Errorf("ArgString(5, DEF) = %q, want default", got)
	}
}

func TestSCPICommand_Raw(t *testing.T) {
	raw := "CONF:VOLT:DC 10,0.001"
	cmd := newSCPICommand(raw, "CONF:VOLT:DC")
	if cmd.String() != raw {
		t.Errorf("String() = %q, want %q", cmd.String(), raw)
	}
}

func TestSCPICommand_WhitespaceTrimmedArgs(t *testing.T) {
	cmd := newSCPICommand("SET  a , b , c ", "SET")
	if len(cmd.Args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(cmd.Args))
	}
	if cmd.Args[0] != "a" || cmd.Args[1] != "b" || cmd.Args[2] != "c" {
		t.Errorf("args not trimmed: %v", cmd.Args)
	}
}
