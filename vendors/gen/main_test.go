package main

import "testing"

func TestCleanName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Synology Incorporated", "Synology"},
		{"Apple, Inc.", "Apple"},
		{"Cisco Systems, Inc", "Cisco Systems"},
		{"Samsung Electronics Co.,Ltd", "Samsung Electronics"},
		{"HUAWEI TECHNOLOGIES CO.,LTD", "HUAWEI TECHNOLOGIES"},
		{"Huawei Device Co., Ltd.", "Huawei Device"},
		{"Intel Corporate", "Intel"},
		{"Raspberry Pi Trading Ltd", "Raspberry Pi Trading"},
		{"Raspberry Pi Foundation", "Raspberry Pi Foundation"},
		{"AVM Audiovisuelles Marketing und Computersysteme GmbH", "AVM Audiovisuelles Marketing und Computersysteme"},
		{"Robert Bosch GmbH & Co. KG", "Robert Bosch"},
		{"Sagemcom Broadband SAS", "Sagemcom Broadband"},
		{"FIBAR GROUP S.A.", "FIBAR GROUP"},
		{"Xiaomi Communications Co Ltd", "Xiaomi Communications"},
		{"Sp. Example sp. z o.o.", "Sp. Example"},
		{"Texas Instruments", "Texas Instruments"},
		{"Cisco", "Cisco"},
		{"  Hewlett   Packard  ", "Hewlett Packard"},
		// A name that is only a legal form stays as it is.
		{"Co., Ltd.", "Co., Ltd."},
	} {
		if got := cleanName(tc.in); got != tc.want {
			t.Errorf("cleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSkipped(t *testing.T) {
	for _, name := range []string{"Private", "IEEE Registration Authority", ""} {
		if !skipped(name) {
			t.Errorf("%q should be skipped", name)
		}
	}
	if skipped("Apple") {
		t.Error("Apple should not be skipped")
	}
}
