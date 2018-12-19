package common

import (
	"fmt"
	"testing"
)

var fakeStr = fmt.Sprintf("Test")

func TestEncoding(t *testing.T) {
	t1 := StructuredApplicationName{
		Name:       "d1-ns",
		DomainList: []string{"test-domain"},
		Owner:      `frozyio`,
	}

	expData := `d1-ns.test-domain.frozyio`
	encData, err := t1.EncodeToString()
	if err != nil {
		t.Errorf("Can't encode application structure due to %v", err)
		return
	}

	if encData != expData {
		t.Errorf("Invalid test results: returned encData: %s, expected: %s", encData, expData)
		return
	}

	t1.Name = "12345"
	encData, err = t1.EncodeToString()
	if err == nil {
		t.Error("Invalid test results: Name with all digits OK")
		return
	}

	t1.Name = "-1db"
	encData, err = t1.EncodeToString()
	if err == nil {
		t.Error("Invalid test results: Name with - at beginning is OK")
		return
	}

	t1.Name = "test"
	t1.Owner = `"exotic@but+valid~email"@gmail.com`
	encData, err = t1.EncodeToString()
	if err != nil {
		t.Errorf("Can't encode application structure due to %v", err)
		return
	}

	expData = `test.test-domain.z-qexotic-abut-lvalid-temail-q-agmail-dcom`
	if encData != expData {
		t.Errorf("Invalid test results: returned encData: %s, expected: %s", encData, expData)
		return
	}

	t1.Owner = `user@domain-with-hyphens.com`
	encData, err = t1.EncodeToString()
	if err != nil {
		t.Errorf("Can't encode application structure due to %v", err)
		return
	}

	expData = `test.test-domain.user-adomain-iwith-ihyphens-dcom`
	if encData != expData {
		t.Errorf("Invalid test results: returned encData: %s, expected: %s", encData, expData)
		return
	}

	t1.Owner = `anton@frozy.io`
	encData, err = t1.EncodeToString()
	if err != nil {
		t.Errorf("Can't encode application structure due to %v", err)
		return
	}

	expData = `test.test-domain.anton-afrozy-dio`
	if encData != expData {
		t.Errorf("Invalid test results: returned encData: %s, expected: %s", encData, expData)
		return
	}

	t1.Owner = `z-order@frozy.io`
	encData, err = t1.EncodeToString()
	if err != nil {
		t.Errorf("Can't encode application structure due to %v", err)
		return
	}

	expData = `test.test-domain.zz-iorder-afrozy-dio`
	if encData != expData {
		t.Errorf("Invalid test results: returned encData: %s, expected: %s", encData, expData)
		return
	}

	t1.Owner = `zz-order@frozy.io`
	encData, err = t1.EncodeToString()
	if err != nil {
		t.Errorf("Can't encode application structure due to %v", err)
		return
	}

	expData = `test.test-domain.zzz-iorder-afrozy-dio`
	if encData != expData {
		t.Errorf("Invalid test results: returned encData: %s, expected: %s", encData, expData)
		return
	}
}

func TestDecoding(t *testing.T) {
	var st ApplicationNameString

	t100 := StructuredApplicationName{
		Name:       "d1-ns",
		DomainList: []string{"test-domain"},
		Owner:      `frozyio`,
	}

	st = `d1-ns.test-domain.frozyio`
	t2, err := DecodeApplicationString(st)
	if err != nil {
		t.Errorf("Can't decode application name due to %v", err)
		return
	}

	if !ApplicationsEqual(&t2, &t100) {
		t.Errorf("Can't decode application name: result %+v, expected: %+v", t2, t100)
		return
	}

	t100.Name = "test"
	t100.Owner = `"exotic@but+valid~email"@gmail.com`
	st = `test.test-domain.z-qexotic-abut-lvalid-temail-q-agmail-dcom`
	t2, err = DecodeApplicationString(st)
	if err != nil {
		t.Errorf("Can't decode application name due to %v", err)
		return
	}

	if !ApplicationsEqual(&t2, &t100) {
		t.Errorf("Can't decode application name: result %+v, expected: %+v", t2, t100)
		return
	}

	t100.Owner = `zz-order@frozy.io`
	st = `test.test-domain.zzz-iorder-afrozy-dio`
	t2, err = DecodeApplicationString(st)
	if err != nil {
		t.Errorf("Can't decode application name due to %v", err)
		return
	}

	if !ApplicationsEqual(&t2, &t100) {
		t.Errorf("Can't decode application name: result %+v, expected: %+v", t2, t100)
		return
	}
}
