package keenetic

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanNdmcErrorConflict(t *testing.T) {
	raw := "\x1b[KNetwork::Interface::Ip error[72220686]: \"Wireguard4\": network 10.2.0.2/32 conflicts with interface \"Wireguard10\". \x1b[K"
	err := cleanNdmcError("interface Wireguard4 up", errors.New("exit status 122"), raw)
	msg := err.Error()
	if strings.Contains(msg, "[K") || strings.Contains(msg, "exit status") || strings.Contains(msg, "error[") {
		t.Fatalf("мусор в ошибке: %q", msg)
	}
	if !strings.Contains(msg, "10.2.0.2/32") || !strings.Contains(msg, "Wireguard10") || !strings.Contains(msg, "отключите") {
		t.Fatalf("нет совета: %q", msg)
	}
}

func TestCleanNdmcErrorGeneric(t *testing.T) {
	raw := "\x1b[KCommand::Base error[7405600]: no such command: status. \x1b[K"
	err := cleanNdmcError("show interface X status", errors.New("exit status 1"), raw)
	if strings.Contains(err.Error(), "[K") || strings.Contains(err.Error(), "exit status") {
		t.Fatalf("мусор: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "no such command") {
		t.Fatalf("потеряно сообщение: %q", err.Error())
	}
}
