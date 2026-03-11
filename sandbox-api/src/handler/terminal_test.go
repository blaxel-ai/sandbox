package handler

import (
	"testing"
)

func TestTerminalCommandBuffer_BasicCommand(t *testing.T) {
	var cb terminalCommandBuffer
	cmds := cb.feed([]byte("ls -la\r"))
	if len(cmds) != 1 || cmds[0] != "ls -la" {
		t.Errorf("expected [\"ls -la\"], got %v", cmds)
	}
}

func TestTerminalCommandBuffer_Backspace(t *testing.T) {
	var cb terminalCommandBuffer
	// Type "lss", backspace, then enter
	cb.feed([]byte("lss"))
	cb.feed([]byte{0x7f}) // DEL
	cmds := cb.feed([]byte("\r"))
	if len(cmds) != 1 || cmds[0] != "ls" {
		t.Errorf("expected [\"ls\"], got %v", cmds)
	}
}

func TestTerminalCommandBuffer_CSISequence(t *testing.T) {
	var cb terminalCommandBuffer
	// "echo hi" with an arrow-up CSI escape in the middle — escape should be stripped
	input := []byte("echo")
	input = append(input, 0x1b, '[', 'A') // ESC [ A = cursor up (CSI)
	input = append(input, []byte(" hi\r")...)
	cmds := cb.feed(input)
	if len(cmds) != 1 || cmds[0] != "echo hi" {
		t.Errorf("expected [\"echo hi\"], got %v", cmds)
	}
}

func TestTerminalCommandBuffer_OSCSequence_BELTerminated(t *testing.T) {
	var cb terminalCommandBuffer
	// "echo test" followed by an OSC title sequence (ESC ] 0 ; title BEL), then Enter
	// The OSC payload "0;title" must NOT leak into the command buffer.
	input := []byte("echo test")
	input = append(input, 0x1b, ']', '0', ';', 't', 'i', 't', 'l', 'e', 0x07) // BEL
	input = append(input, '\r')
	cmds := cb.feed(input)
	if len(cmds) != 1 || cmds[0] != "echo test" {
		t.Errorf("expected [\"echo test\"], got %v", cmds)
	}
}

func TestTerminalCommandBuffer_OSCSequence_STTerminated(t *testing.T) {
	var cb terminalCommandBuffer
	// OSC terminated by ST (ESC \) instead of BEL
	input := []byte("pwd")
	input = append(input, 0x1b, ']', '0', ';', 't', 'i', 't', 'l', 'e', 0x1b, '\\') // ST
	input = append(input, '\r')
	cmds := cb.feed(input)
	if len(cmds) != 1 || cmds[0] != "pwd" {
		t.Errorf("expected [\"pwd\"], got %v", cmds)
	}
}

func TestTerminalCommandBuffer_MultipleCommands(t *testing.T) {
	var cb terminalCommandBuffer
	cmds := cb.feed([]byte("echo a\recho b\r"))
	if len(cmds) != 2 || cmds[0] != "echo a" || cmds[1] != "echo b" {
		t.Errorf("expected [\"echo a\", \"echo b\"], got %v", cmds)
	}
}

func TestTerminalCommandBuffer_EmptyLine(t *testing.T) {
	var cb terminalCommandBuffer
	// Pressing Enter on an empty line should produce no commands
	cmds := cb.feed([]byte("\r"))
	if len(cmds) != 0 {
		t.Errorf("expected no commands for empty line, got %v", cmds)
	}
}

func TestTerminalCommandBuffer_TwoCharEscape(t *testing.T) {
	var cb terminalCommandBuffer
	// ESC M (reverse index) is a two-char escape — should not corrupt buffer
	input := []byte("cd /tmp")
	input = append(input, 0x1b, 'M') // ESC M
	input = append(input, '\r')
	cmds := cb.feed(input)
	if len(cmds) != 1 || cmds[0] != "cd /tmp" {
		t.Errorf("expected [\"cd /tmp\"], got %v", cmds)
	}
}
