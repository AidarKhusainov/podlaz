package sub

import (
 "strings"
 "testing"

 "github.com/AidarKhusainov/podlaz/internal/profile"
)

const mihomoExistingFixture = `proxies:
  - name: vmess
    type: vmess
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    alterId: 0
    cipher: auto
    tls: true
    servername: vpn.example.com
    network: ws
    ws-opts:
      path: /api
      headers:
        Host: edge.example.com
  - name: trojan
    type: trojan
    server: vpn.example.com
    port: 443
    password: example-trojan-password
    sni: vpn.example.com
    network: grpc
    grpc-opts:
      grpc-service-name: api
  - name: shadowsocks
    type: ss
    server: vpn.example.com
    port: 8388
    cipher: aes-128-gcm
    password: example-shadowsocks-password
  - name: vless
    type: vless
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`

func TestMihomoExistingProtocolsLocalAndSubscriptionImport(t *testing.T) {
 local,err:=ParseLocalImportContent([]byte(mihomoExistingFixture))
 if err!=nil {t.Fatal(err)}
 format,remote,err:=ParseSubscriptionContent([]byte(mihomoExistingFixture))
 if err!=nil {t.Fatal(err)}
 if format!=FormatMihomo || len(local.Profiles)!=4 || len(remote.Profiles)!=4 {t.Fatalf("unexpected import count/format: %s %d %d",format,len(local.Profiles),len(remote.Profiles))}
 byProtocol:=map[string]profile.Profile{}
 for i,p:=range local.Profiles {
  if p.ID!=remote.Profiles[i].ID {t.Fatal("local/remote identity mismatch")}
  byProtocol[p.Protocol]=p
  if p.Source!=profile.SourceImportedFile {t.Fatalf("unexpected source: %s",p.Source)}
 }
 if byProtocol["vmess"].Transport!="ws" || byProtocol["vmess"].Encryption!="auto" || byProtocol["vmess"].Path!="/api" ||
 byProtocol["trojan"].Security!="tls" || byProtocol["trojan"].ServiceName!="api" ||
 byProtocol["shadowsocks"].Encryption!="aes-128-gcm" || byProtocol["vless"].Protocol!="vless" {t.Fatalf("incorrect protocol mapping")}
}

func TestMihomoExistingProtocolsRejectBehaviorChangingOptions(t *testing.T) {
 const secret="provider-secret-must-stay-redacted"
 for _,tt:=range []struct{name,fragment,extra string}{
  {"vmess legacy","vmess", "    alterId: 1\n"},
  {"vmess header","vmess", "    header: "+secret+"\n"},
  {"vmess packet","vmess", "    packet-encoding: xudp\n"},
  {"vmess cipher","vmess", "    cipher: unsupported\n"},
  {"trojan tls","trojan", "    tls: false\n"},
  {"trojan insecure","trojan", "    skip-cert-verify: true\n"},
  {"trojan plugin","trojan", "    ss-opts:\n      enabled: true\n"},
  {"ss plugin","ss", "    plugin: obfs\n"},
  {"ss cipher","ss", "    cipher: unsupported\n"},
 }{
  t.Run(tt.name,func(t *testing.T){
   var base string
   switch tt.fragment {
   case "vmess":base="    uuid: 00000000-0000-0000-0000-000000000002\n    cipher: auto\n"
   case "trojan","ss":base="    password: example-password\n";if tt.fragment=="ss"{base+="    cipher: aes-128-gcm\n"}
   }
   if strings.Contains(tt.extra,"    cipher:") || strings.Contains(tt.extra,"    alterId:"){base=strings.ReplaceAll(base,"    cipher: auto\n","");base=strings.ReplaceAll(base,"    cipher: aes-128-gcm\n","")}
   input:="proxies:\n  - name: example\n    type: "+tt.fragment+"\n    server: vpn.example.com\n    port: 443\n"+base+tt.extra
   _,err:=ParseLocalImportContent([]byte(input))
   if err==nil {t.Fatal("expected strict rejection")}
   if strings.Contains(err.Error(),secret) || strings.Contains(err.Error(),"example-password"){t.Fatal("error leaked credentials")}
  })
 }
}

func TestMihomoExistingProtocolsRejectDuplicateIdentity(t *testing.T) {
 input:=strings.Replace(mihomoExistingFixture,"  - name: trojan", "  - name: vmess-copy\n    type: vmess\n    server: vpn.example.com\n    port: 443\n    uuid: 00000000-0000-0000-0000-000000000002\n    alterId: 0\n    cipher: auto\n    tls: true\n    servername: vpn.example.com\n    network: ws\n    ws-opts:\n      path: /api\n      headers:\n        Host: edge.example.com\n  - name: trojan",1)
 _,err:=ParseLocalImportContent([]byte(input))
 if err==nil || !strings.Contains(err.Error(),"duplicate Clash/Mihomo profile id"){t.Fatalf("expected duplicate rejection: %v",err)}
}
