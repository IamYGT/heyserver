package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFirewallControllerInventoriesMutatesAndProtectsConfiguredPaths(t *testing.T) {
	const managed = "-A HSERVER-INPUT -s 203.0.113.0/24 -p tcp --dport 443 -m comment --comment hserver:fw-0123456789ab:cHJvb2Y -j ACCEPT"
	runner := &fakeRunner{outputs: [][]byte{
		[]byte("-P INPUT DROP\n-A INPUT -p tcp --dport 22 -j ACCEPT\n-A INPUT -j HSERVER-INPUT\n"),
		[]byte("-N HSERVER-INPUT\n" + managed + "\n"),
		[]byte("active\n"),
	}}
	controller := newFirewallController(runner, true, true, "/usr/sbin/iptables", "/usr/sbin/netfilter-persistent", filepath.Join(t.TempDir(), "firewall.lock"), "netfilter-persistent.service", []string{"198.51.100.10/32"}, map[int]struct{}{22: {}})
	inventory, err := controller.Inventory(context.Background())
	if err != nil || inventory.Policy != "DROP" || inventory.Persistence != "active" || len(inventory.Rules) != 2 || inventory.Rules[0].ID != "fw-0123456789ab" || inventory.Rules[0].Comment != "proof" {
		t.Fatalf("Inventory = (%#v, %v)", inventory, err)
	}
	if !reflect.DeepEqual(inventory.ProtectedSources, []string{"198.51.100.10/32"}) || !reflect.DeepEqual(inventory.ProtectedPorts, []int{22}) {
		t.Fatalf("protection metadata = %#v %#v", inventory.ProtectedSources, inventory.ProtectedPorts)
	}

	protectedRunner := &fakeRunner{}
	protected := newFirewallController(protectedRunner, true, true, "/usr/sbin/iptables", "/usr/sbin/netfilter-persistent", filepath.Join(t.TempDir(), "firewall.lock"), "netfilter-persistent.service", []string{"198.51.100.10/32"}, map[int]struct{}{22: {}})
	if _, err := protected.Add(context.Background(), firewallRule{Action: "DROP", Protocol: "tcp", Port: 22, Source: "198.51.100.0/24"}, firewallRevision(nil)); !errors.Is(err, errFirewallProtected) {
		t.Fatalf("protected Add error = %v", err)
	}
	if len(protectedRunner.commands) != 0 {
		t.Fatalf("protected rule executed commands: %#v", protectedRunner.commands)
	}

	addRunner := &fakeRunner{outputs: [][]byte{[]byte("-N HSERVER-INPUT\n"), nil, nil, nil}}
	adder := newFirewallController(addRunner, true, true, "/usr/sbin/iptables", "/usr/sbin/netfilter-persistent", filepath.Join(t.TempDir(), "firewall.lock"), "netfilter-persistent.service", nil, nil)
	id, err := adder.Add(context.Background(), firewallRule{Action: "ACCEPT", Protocol: "tcp", Port: 443, Source: "203.0.113.9", Comment: "web"}, firewallRevision([]string{}))
	if err != nil || !agentFirewallIDPattern.MatchString(id) {
		t.Fatalf("Add = (%q, %v)", id, err)
	}
	joined := strings.Join(addRunner.commands[2].args, " ")
	if addRunner.commands[2].name != "/usr/sbin/iptables" || !strings.Contains(joined, "-s 203.0.113.9/32 -p tcp --dport 443") || !strings.Contains(joined, "hserver:"+id+":") || addRunner.commands[3].name != "/usr/sbin/netfilter-persistent" {
		t.Fatalf("add commands = %#v", addRunner.commands)
	}
}

func TestFirewallControllerRollsBackWhenPersistenceFails(t *testing.T) {
	runner := &fakeRunner{
		outputs: [][]byte{[]byte("-N HSERVER-INPUT\n"), nil, nil, nil, nil},
		errors:  []error{nil, nil, nil, errors.New("save failed"), nil},
	}
	controller := newFirewallController(runner, true, true, "/usr/sbin/iptables", "/usr/sbin/netfilter-persistent", filepath.Join(t.TempDir(), "firewall.lock"), "netfilter-persistent.service", nil, nil)
	_, err := controller.Add(context.Background(), firewallRule{Action: "ACCEPT", Protocol: "all"}, firewallRevision([]string{}))
	if !errors.Is(err, errFirewallPersistence) {
		t.Fatalf("Add error = %v", err)
	}
	if len(runner.commands) != 5 || runner.commands[4].args[0] != "-D" || runner.commands[4].args[1] != firewallManagedChain {
		t.Fatalf("rollback commands = %#v", runner.commands)
	}
}
