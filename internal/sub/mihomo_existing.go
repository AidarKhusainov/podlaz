package sub

import (
 "encoding/base64"
 "encoding/json"
 "fmt"
 "net"
 "net/url"
 "strconv"
 "strings"

 "github.com/AidarKhusainov/podlaz/internal/profile"
 "go.yaml.in/yaml/v3"
)

// These decoders deliberately accept only a subset that maps to the existing
// share-URI importers. Unrecognized knobs must never be silently discarded.
func mihomoExisting(entry *yaml.Node, fields map[string]*yaml.Node, source profile.SourceType, kind string) (profile.Profile, []string, error) {
 allowed := map[string]bool{"name":true,"type":true,"server":true,"port":true}
 switch kind {
 case "vmess":
  for _, k := range []string{"uuid","alterId","cipher","network","tls","servername","sni","alpn","client-fingerprint","ws-opts"} { allowed[k]=true }
 case "trojan":
  for _, k := range []string{"password","network","tls","sni","servername","alpn","client-fingerprint","ws-opts","grpc-opts"} { allowed[k]=true }
 case "ss":
  for _, k := range []string{"password","cipher"} { allowed[k]=true }
 }
 for i:=0;i<len(entry.Content);i+=2 {
  key:=entry.Content[i]
  if !allowed[key.Value] { return profile.Profile{},nil,fmt.Errorf("unsupported Clash/Mihomo %s option at line %d",kind,key.Line) }
 }
 name,err:=mihomoRequiredString(fields,"name",entry.Line)
 if err!=nil { return profile.Profile{},nil,err }
 host,err:=mihomoRequiredString(fields,"server",entry.Line)
 if err!=nil { return profile.Profile{},nil,err }
 pn,ok:=fields["port"]
 if !ok || pn.Kind!=yaml.ScalarNode || pn.Tag!="!!int" { return profile.Profile{},nil,fmt.Errorf("malformed Clash/Mihomo YAML: port must be an integer at line %d",entry.Line) }
 port,err:=strconv.ParseUint(pn.Value,10,16)
 if err!=nil || port==0 { return profile.Profile{},nil,fmt.Errorf("malformed Clash/Mihomo YAML: port must be between 1 and 65535 at line %d",pn.Line) }
 endpoint:=net.JoinHostPort(host,strconv.FormatUint(port,10))
 var p profile.Profile
 var warnings []string
 switch kind {
 case "ss":
  method,err:=mihomoRequiredString(fields,"cipher",entry.Line);if err!=nil{return p,nil,err}
  password,err:=mihomoRequiredString(fields,"password",entry.Line);if err!=nil{return p,nil,err}
  // Avoid importing methods supported by Mihomo but not established for this Xray path.
  switch method {
  case "aes-128-gcm","aes-256-gcm","chacha20-ietf-poly1305","chacha20-poly1305":
  default:return p,nil,fmt.Errorf("unsupported Clash/Mihomo ss cipher at line %d",entry.Line)
  }
  credentials:=base64.RawURLEncoding.EncodeToString([]byte(method+":"+password))
  link:="ss://"+credentials+"@"+endpoint+"#"+url.QueryEscape(name)
  p,warnings,err=profile.ImportShadowsocksURI(link)
  if err!=nil {return profile.Profile{},nil,fmt.Errorf("invalid Clash/Mihomo ss profile at line %d",entry.Line)}
 case "vmess","trojan":
  network,err:=mihomoOptionalString(fields,"network");if err!=nil{return p,nil,err}
  switch network {case "","tcp","ws":case "grpc":if kind=="vmess" {return p,nil,fmt.Errorf("unsupported Clash/Mihomo vmess network at line %d",entry.Line)}
  default:return p,nil,fmt.Errorf("unsupported Clash/Mihomo %s network at line %d",kind,entry.Line)}
  tls:=kind=="trojan"
  if v,ok:=fields["tls"];ok{
   if v.Kind!=yaml.ScalarNode || v.Tag!="!!bool" {return p,nil,fmt.Errorf("malformed Clash/Mihomo YAML: tls must be a boolean at line %d",v.Line)}
   tls,err=strconv.ParseBool(v.Value);if err!=nil{return p,nil,fmt.Errorf("malformed Clash/Mihomo YAML: invalid tls at line %d",v.Line)}
  }
  if kind=="trojan" && !tls {return p,nil,fmt.Errorf("unsupported Clash/Mihomo trojan without TLS at line %d",entry.Line)}
  if fields["sni"]!=nil && fields["servername"]!=nil {return p,nil,fmt.Errorf("unsupported Clash/Mihomo %s ambiguous SNI at line %d",kind,entry.Line)}
  sni,err:=mihomoOptionalString(fields,"sni");if err!=nil{return p,nil,err}
  if fields["servername"]!=nil{sni,err=mihomoOptionalString(fields,"servername");if err!=nil{return p,nil,err}}
  fp,err:=mihomoOptionalString(fields,"client-fingerprint");if err!=nil{return p,nil,err}
  q:=url.Values{}
  if network=="" {network="tcp"}
  q.Set("type",network)
  if tls {q.Set("security","tls")} else {q.Set("security","none")}
  if sni!=""{q.Set("sni",sni)}
  if fp!=""{q.Set("fp",fp)}
  if node:=fields["alpn"];node!=nil {
   if node.Kind!=yaml.SequenceNode{return p,nil,fmt.Errorf("malformed Clash/Mihomo YAML: alpn must be a list at line %d",node.Line)}
   var values []string
   for _,child:=range node.Content {
    value,e:=mihomoString(child,"alpn");if e!=nil{return p,nil,e}
    if value=="" || strings.Contains(value,","){return p,nil,fmt.Errorf("unsupported Clash/Mihomo alpn at line %d",child.Line)}
    values=append(values,value)
   }
   q.Set("alpn",strings.Join(values,","))
  }
  if node:=fields["ws-opts"];node!=nil {
   if network!="ws"{return p,nil,fmt.Errorf("unsupported Clash/Mihomo ws-opts without ws at line %d",node.Line)}
   opts,e:=mihomoMapping(node);if e!=nil{return p,nil,e}
   for i:=0;i<len(node.Content);i+=2 {
    k:=node.Content[i]
    if k.Value!="path" && k.Value!="headers"{return p,nil,fmt.Errorf("unsupported Clash/Mihomo ws-opts option at line %d",k.Line)}
   }
   if path:=opts["path"];path!=nil{v,e:=mihomoString(path,"ws-opts.path");if e!=nil{return p,nil,e};q.Set("path",v)}
   if headers:=opts["headers"];headers!=nil{
    h,e:=mihomoMapping(headers);if e!=nil{return p,nil,e}
    if len(h)!=1 || h["Host"]==nil{return p,nil,fmt.Errorf("unsupported Clash/Mihomo ws-opts.headers at line %d",headers.Line)}
    v,e:=mihomoString(h["Host"],"ws-opts.headers.Host");if e!=nil{return p,nil,e};q.Set("host",v)
   }
  }
  if node:=fields["grpc-opts"];node!=nil {
   if network!="grpc"{return p,nil,fmt.Errorf("unsupported Clash/Mihomo grpc-opts without grpc at line %d",node.Line)}
   if _,e:=mihomoStrictOptions(node,map[string]string{"grpc-service-name":"serviceName"},q);e!=nil{return p,nil,e}
  }
  if kind=="trojan" {
   password,e:=mihomoRequiredString(fields,"password",entry.Line);if e!=nil{return p,nil,e}
   link:=(&url.URL{Scheme:"trojan",User:url.User(password),Host:endpoint,RawQuery:q.Encode(),Fragment:name}).String()
   p,warnings,err=profile.ImportTrojanURI(link)
  } else {
   uuid,e:=mihomoRequiredString(fields,"uuid",entry.Line);if e!=nil{return p,nil,e}
   cipher,e:=mihomoRequiredString(fields,"cipher",entry.Line);if e!=nil{return p,nil,e}
   switch cipher {case "auto","none","aes-128-gcm","chacha20-poly1305":default:return p,nil,fmt.Errorf("unsupported Clash/Mihomo vmess cipher at line %d",entry.Line)}
   alter:=fields["alterId"]
   if alter!=nil {
    if alter.Kind!=yaml.ScalarNode || alter.Tag!="!!int" || alter.Value!="0" {
     return p,nil,fmt.Errorf("unsupported Clash/Mihomo vmess alterId at line %d",alter.Line)
    }
   }
   payload:=map[string]string{"v":"2","ps":name,"add":host,"port":strconv.FormatUint(port,10),"id":uuid,"aid":"0","scy":cipher,"net":network,"type":"none","sni":sni,"fp":fp,"tls":"","host":q.Get("host"),"path":q.Get("path"),"alpn":q.Get("alpn")}
   if tls {payload["tls"]="tls"}
   raw,_:=json.Marshal(payload)
   p,warnings,err=profile.ImportVMessURI("vmess://"+base64.StdEncoding.EncodeToString(raw))
  }
  if err!=nil{return profile.Profile{},nil,fmt.Errorf("invalid Clash/Mihomo %s profile at line %d",kind,entry.Line)}
 default:
  return p,nil,fmt.Errorf("unsupported Clash/Mihomo proxy protocol at line %d",entry.Line)
 }
 p.Source=source
 return p,warnings,nil
}
