//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

type scriptedNML3Mutation struct {
	add func(context.Context, map[string]map[string]dbus.Variant) (dbus.ObjectPath, error)
	activate func(context.Context, dbus.ObjectPath) (dbus.ObjectPath, error)
}
func (m scriptedNML3Mutation) Add(ctx context.Context, config map[string]map[string]dbus.Variant) (dbus.ObjectPath, error) {
	return m.add(ctx, config)
}
func (m scriptedNML3Mutation) Activate(ctx context.Context, path dbus.ObjectPath) (dbus.ObjectPath, error) {
	return m.activate(ctx, path)
}
func testNML3Plan() nmL3Plan {
	return nmL3Plan{Interface:"podlaz0", MTU:1500, IPv4:"198.18.0.17/32", DNS:"1.1.1.1", Metric:10}
}
func readNML3Intent(t *testing.T, s nmL3Stager) nmL3Intent {
	t.Helper()
	data, err := os.ReadFile(s.path())
	if err != nil { t.Fatal(err) }
	var out nmL3Intent
	if err := json.Unmarshal(data, &out); err != nil { t.Fatal(err) }
	return out
}

func TestNML3StagesInMemoryProfileAndActivationWithDurableAuthority(t *testing.T) {
	dir := t.TempDir()
	state := protectedNMTestSession(t, dir)
	stager := newNML3Stager(dir, fixedBootID("boot-a"))
	calls := []string{}
	mutation := scriptedNML3Mutation{
		add:func(_ context.Context, settings map[string]map[string]dbus.Variant) (dbus.ObjectPath,error) {
			calls = append(calls,"add")
			observed := readNML3Intent(t,stager)
			if observed.Stage != "planned" || observed.UUID == "" || observed.SessionID != state.SessionID {
				t.Fatalf("activation happened without exact durable plan: %#v",observed)
			}
			if settings["connection"]["autoconnect"].Value() != false ||
				settings["connection"]["type"].Value() != "tun" ||
				settings["tun"]["mode"].Value() != uint32(1) ||
				settings["tun"]["pi"].Value() != false ||
				settings["ipv4"]["route-table"].Value() != uint32(254) ||
				settings["ipv4"]["dns-priority"].Value() != int32(-30000) ||
				settings["ipv6"]["method"].Value() != "disabled" {
				t.Fatal("NM settings lost link/L3/DNS exclusive authority or autoconnect guard")
			}
			if settings["connection"]["uuid"].Value() != observed.UUID { t.Fatal("NM UUID differs from durable intent") }
			if _, err := os.Stat(stager.path()); err != nil { t.Fatal(err) }
			return "/org/freedesktop/NetworkManager/Settings/7",nil
		},
		activate:func(_ context.Context,path dbus.ObjectPath)(dbus.ObjectPath,error){
			calls = append(calls,"activate")
			observed := readNML3Intent(t,stager)
			if observed.Stage != "profile-added" || observed.SettingsPath != path {
				t.Fatalf("activation before exact profile path persisted: %#v",observed)
			}
			return "/org/freedesktop/NetworkManager/ActiveConnection/12",nil
		},
	}
	entry, err := stager.stageWith(context.Background(), state.SessionID, 101, testNML3Plan(), mutation, func(string)error {
		calls=append(calls,"no-collision")
		return nil
	})
	if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(calls, []string{"no-collision","add","activate"}) {
		t.Fatalf("mutating NM call ordering=%v",calls)
	}
	if entry.Stage != "activating" || entry.ActivePath == "" {
		t.Fatalf("incomplete activation record: %#v", entry)
	}
	if got := readNML3Intent(t,stager); !reflect.DeepEqual(entry,got) {
		t.Fatalf("activation record not durable: %#v vs %#v",entry,got)
	}
	if info, err := os.Stat(stager.path()); err != nil || info.Mode().Perm()!=0o600 {
		t.Fatalf("NM intent not private: %v %v",info,err)
	}
	if _, err := stager.stageWith(context.Background(),state.SessionID,101,testNML3Plan(),mutation,func(string)error { t.Fatal("duplicate did network inspection"); return nil }); err == nil {
		t.Fatal("second activation adopted or replaced previous NM intent")
	}
}

func TestNML3FailureNeverCleansAmbiguousIntentOrRetries(t *testing.T) {
	for _, tc := range []struct{
		name string
		failAdd bool
	}{
		{"add-timeout",true}, {"activate-timeout",false},
	} {
		t.Run(tc.name,func(t *testing.T){
			dir:=t.TempDir()
			session:=protectedNMTestSession(t,dir)
			stage:=newNML3Stager(dir,fixedBootID("boot-a"))
			failure:=errors.New("ambiguous NM D-Bus operation")
			activationCalled:=false
			mutation:=scriptedNML3Mutation{
				add:func(context.Context,map[string]map[string]dbus.Variant)(dbus.ObjectPath,error){
					if tc.failAdd { return "",failure }
					return "/org/freedesktop/NetworkManager/Settings/5",nil
				},
				activate:func(context.Context,dbus.ObjectPath)(dbus.ObjectPath,error){
					activationCalled=true
					return "",failure
				},
			}
			_,err:=stage.stageWith(context.Background(),session.SessionID,101,testNML3Plan(),mutation,func(string)error{return nil})
			if !errors.Is(err,failure) { t.Fatalf("lost true NM error: %v",err) }
			if activationCalled==tc.failAdd { t.Fatalf("activation after incomplete Add: called=%v",activationCalled) }
			persisted:=readNML3Intent(t,stage)
			expected:="planned"
			if !tc.failAdd { expected="profile-added" }
			if persisted.Stage!=expected { t.Fatalf("ambiguous phase was erased: %#v",persisted) }
		})
	}
}

func TestNML3RefusesNoPrivacyOrForeignLinkBeforeMutation(t *testing.T) {
	for _, tc:=range []struct{name string; mark uint32; change func(*networkSessionState); occupied bool}{
		{"wrong-mark",102,func(*networkSessionState){},false},
		{"unarmed",101,func(s *networkSessionState){s.Protection.State=networkSessionProtectionUnarmed},false},
		{"terminal",101,func(s *networkSessionState){s.Intent=networkSessionIntentTerminal},false},
		{"other-session",101,func(s *networkSessionState){s.SessionID="00112233445566778899aabbccddeeff"},false},
		{"foreign-link",101,func(*networkSessionState){},true},
	}{
		t.Run(tc.name,func(t *testing.T){
			dir:=t.TempDir()
			session:=protectedNMTestSession(t,dir)
			store:=newNetworkSessionStateStore(dir,fixedBootID("boot-a"))
			if _,_,err:=store.Update(func(s *networkSessionState)error{tc.change(s);return nil});err!=nil{t.Fatal(err)}
			called:=false
			failName:=func(string)error {
				if tc.occupied{return errors.New("foreign link")}
				return nil
			}
			mutation:=scriptedNML3Mutation{
				add:func(context.Context,map[string]map[string]dbus.Variant)(dbus.ObjectPath,error){called=true;return "/",nil},
				activate:func(context.Context,dbus.ObjectPath)(dbus.ObjectPath,error){called=true;return "/",nil},
			}
			stage:=newNML3Stager(dir,fixedBootID("boot-a"))
			_,err:=stage.stageWith(context.Background(),session.SessionID,tc.mark,testNML3Plan(),mutation,failName)
			if err==nil||called {t.Fatalf("foreign/unprotected NM mutation: err=%v called=%v",err,called)}
			if _,err:=os.Stat(stage.path()); !errors.Is(err,os.ErrNotExist){t.Fatalf("intent persisted before accepted admission: %v",err)}
		})
	}
}

func TestNML3RejectsInvalidPlanWithoutNMOrHostChanges(t *testing.T){
	plan:=testNML3Plan()
	for _,tc:=range []struct{name string; change func(*nmL3Plan)}{
		{"no-ifname",func(p *nmL3Plan){p.Interface=""}},
		{"invalid-MTU",func(p *nmL3Plan){p.MTU=0}},
		{"default-route-metric",func(p *nmL3Plan){p.Metric=0}},
		{"IPv6-address",func(p *nmL3Plan){p.IPv4="2001:db8::1/128"}},
		{"bad-DNS",func(p *nmL3Plan){p.DNS="127.0.0.1"}},
	}{
		t.Run(tc.name,func(t *testing.T){
			p:=plan
			tc.change(&p)
			if err:=validateNML3Plan(p);err==nil{t.Fatal("invalid NM plan admitted")}
		})
	}
}
