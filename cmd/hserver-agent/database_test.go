package main

import (
	"context"
	"reflect"
	"testing"
)

func TestDatabaseControllerInventoriesAndRestartsFixedEngines(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{
		[]byte("11.4.5-MariaDB\n"),
		[]byte("app\t4096\t12\t3\n"),
		[]byte("42\tapp\tapp\tQuery\t7\trunning\tselect 1\n"),
		[]byte("active\n"),
		[]byte("16 main 5432 online postgres /var/lib/postgresql/16/main\n15 old 5433 down postgres /var/lib/postgresql/15/old\n"),
		[]byte("16.4\n"),
		[]byte("app\t8192\t0\t4\n"),
		[]byte("84\tpostgres\tapp\tactive\t11\tselect 1\n"),
		[]byte("active\n"),
		nil, nil,
		[]byte("16 main 5432 online postgres /var/lib/postgresql/16/main\n"), nil, nil,
	}}
	controller := newDatabaseController(runner, true, map[string]struct{}{"mariadb": {}, "postgresql": {}}, "/usr/bin/mariadb", "/usr/bin/mariadb-admin", "/usr/bin/pg_lsclusters", "/usr/bin/psql", "/usr/bin/pg_isready", "/usr/sbin/runuser")
	engines, err := controller.Inventory(context.Background())
	if err != nil || len(engines) != 2 {
		t.Fatalf("Inventory = (%#v, %v)", engines, err)
	}
	if engines[0].ID != "mariadb" || engines[0].DataSize != 4096 || engines[0].Databases[0].Objects != 12 || engines[0].Sessions[0].State != "Query · running" {
		t.Fatalf("MariaDB = %#v", engines[0])
	}
	if engines[1].ID != "postgresql" || engines[1].Unit != "postgresql@16-main.service" || engines[1].DataSize != 8192 || engines[1].Databases[0].Connections != 4 || engines[1].Sessions[0].Age != 11 {
		t.Fatalf("PostgreSQL = %#v", engines[1])
	}
	if message, err := controller.Action(context.Background(), "mariadb", "restart"); err != nil || message != "MariaDB restarted and socket health check passed" {
		t.Fatalf("MariaDB restart = (%q, %v)", message, err)
	}
	if message, err := controller.Action(context.Background(), "postgresql", "restart"); err != nil || message != "PostgreSQL restarted and readiness check passed" {
		t.Fatalf("PostgreSQL restart = (%q, %v)", message, err)
	}
	commands := runner.commands
	if len(commands) != 14 || commands[9].name != "systemctl" || !reflect.DeepEqual(commands[9].args, []string{"restart", "mariadb.service"}) || commands[10].name != "/usr/bin/mariadb-admin" || commands[12].name != "systemctl" || !reflect.DeepEqual(commands[12].args, []string{"restart", "postgresql@16-main.service"}) || commands[13].name != "/usr/bin/pg_isready" {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestDatabaseControllerRequiresLocalOptIns(t *testing.T) {
	controller := newDatabaseController(&fakeRunner{}, false, nil, "/usr/bin/mariadb", "/usr/bin/mariadb-admin", "/usr/bin/pg_lsclusters", "/usr/bin/psql", "/usr/bin/pg_isready", "/usr/sbin/runuser")
	if _, err := controller.Inventory(context.Background()); err == nil {
		t.Fatal("Inventory succeeded without read opt-in")
	}
	if _, err := controller.Action(context.Background(), "mariadb", "restart"); err == nil {
		t.Fatal("restart succeeded without engine allowlist")
	}
}

func TestSelectPostgresClusterUsesHighestOnlineVersion(t *testing.T) {
	cluster, ok := selectPostgresCluster("15 main 5432 online\n17 main 5434 down\n16 primary 5433 online\n")
	if !ok || cluster.version != "16" || cluster.name != "primary" || cluster.port != "5433" {
		t.Fatalf("cluster = %#v, %t", cluster, ok)
	}
}
