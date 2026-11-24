package storage

import (
	"net"
	"testing"
)

func TestDeviceNames(t *testing.T) {
	db := openTestDB(t)
	laptop, _ := net.ParseMAC(laptopMAC)
	nas, _ := net.ParseMAC(nasMAC)

	names, err := GetDeviceNames(db)
	if err != nil || len(names) != 0 {
		t.Fatalf("names = %v, %v; want none", names, err)
	}

	if err := SetDeviceName(db, laptop, "Work laptop"); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceName(db, nas, "NAS"); err != nil {
		t.Fatal(err)
	}
	// Setting a name again replaces it.
	if err := SetDeviceName(db, laptop, "Anna's laptop"); err != nil {
		t.Fatal(err)
	}

	names, err = GetDeviceNames(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[laptopMAC] != "Anna's laptop" || names[nasMAC] != "NAS" {
		t.Errorf("names = %v", names)
	}

	if err := DeleteDeviceName(db, laptop); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDeviceName(db, laptop); err != nil {
		t.Errorf("deleting a missing name: %v", err)
	}
	names, _ = GetDeviceNames(db)
	if len(names) != 1 || names[nasMAC] != "NAS" {
		t.Errorf("after delete, names = %v; want only the NAS", names)
	}
}
