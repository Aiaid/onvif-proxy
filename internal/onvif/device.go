package onvif

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Aiaid/onvif-proxy/internal/soap"
)

// handleDevice dispatches POST /onvif/device_service.
func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	req, err := parse(r)
	if err != nil {
		soap.WriteInvalidArg(w, "malformed SOAP request")
		return
	}
	if !s.authorized(req, req.Action) {
		soap.WriteNotAuthorized(w)
		return
	}

	switch req.Action {
	case "GetSystemDateAndTime":
		write(w, systemDateAndTime())
	case "SetSystemDateAndTime":
		write(w, "<tds:SetSystemDateAndTimeResponse/>")
	case "GetCapabilities":
		write(w, s.capabilities(r))
	case "GetServices":
		write(w, s.services(r, includeCapability(req.Body)))
	case "GetServiceCapabilities":
		write(w, deviceServiceCapabilities())
	case "GetDeviceInformation":
		write(w, s.deviceInformation())
	case "GetScopes":
		write(w, s.scopes())
	case "GetNetworkInterfaces":
		write(w, s.networkInterfaces(r))
	case "GetNetworkProtocols":
		write(w, s.networkProtocols())
	case "GetHostname":
		write(w, s.hostnameInfo())
	case "GetDNS":
		write(w, "<tds:GetDNSResponse><tds:DNSInformation><tt:FromDHCP>true</tt:FromDHCP></tds:DNSInformation></tds:GetDNSResponse>")
	case "GetNTP":
		write(w, "<tds:GetNTPResponse><tds:NTPInformation><tt:FromDHCP>true</tt:FromDHCP></tds:NTPInformation></tds:GetNTPResponse>")
	case "GetUsers":
		write(w, s.users())
	case "GetWsdlUrl":
		write(w, "<tds:GetWsdlUrlResponse><tds:WsdlUrl>http://www.onvif.org/</tds:WsdlUrl></tds:GetWsdlUrlResponse>")
	case "SystemReboot":
		write(w, "<tds:SystemRebootResponse><tds:Message>Rebooting...</tds:Message></tds:SystemRebootResponse>")
	default:
		soap.WriteActionNotSupported(w, req.Action)
	}
}

func systemDateAndTime() string {
	now := time.Now().UTC()
	dt := func(t time.Time) string {
		return fmt.Sprintf(
			"<tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>"+
				"<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date>",
			t.Hour(), t.Minute(), t.Second(), t.Year(), int(t.Month()), t.Day())
	}
	return "<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime>" +
		"<tt:DateTimeType>NTP</tt:DateTimeType>" +
		"<tt:DaylightSavings>false</tt:DaylightSavings>" +
		"<tt:TimeZone><tt:TZ>UTC0</tt:TZ></tt:TimeZone>" +
		"<tt:UTCDateTime>" + dt(now) + "</tt:UTCDateTime>" +
		"<tt:LocalDateTime>" + dt(now) + "</tt:LocalDateTime>" +
		"</tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>"
}

func (s *Server) capabilities(r *http.Request) string {
	deviceXAddr := soap.XMLEscape("http://" + s.hostPort(r) + "/onvif/device_service")
	mediaXAddr := soap.XMLEscape("http://" + s.hostPort(r) + "/onvif/media_service")
	return "<tds:GetCapabilitiesResponse><tds:Capabilities>" +
		"<tt:Device>" +
		"<tt:XAddr>" + deviceXAddr + "</tt:XAddr>" +
		"<tt:Network><tt:IPFilter>false</tt:IPFilter><tt:ZeroConfiguration>false</tt:ZeroConfiguration><tt:IPVersion6>false</tt:IPVersion6><tt:DynDNS>false</tt:DynDNS></tt:Network>" +
		"<tt:System><tt:DiscoveryResolve>false</tt:DiscoveryResolve><tt:DiscoveryBye>true</tt:DiscoveryBye><tt:RemoteDiscovery>false</tt:RemoteDiscovery><tt:SystemBackup>false</tt:SystemBackup><tt:SystemLogging>false</tt:SystemLogging><tt:FirmwareUpgrade>false</tt:FirmwareUpgrade></tt:System>" +
		"<tt:Security><tt:TLS1.1>false</tt:TLS1.1><tt:TLS1.2>false</tt:TLS1.2><tt:OnboardKeyGeneration>false</tt:OnboardKeyGeneration><tt:AccessPolicyConfig>false</tt:AccessPolicyConfig><tt:X.509Token>false</tt:X.509Token><tt:SAMLToken>false</tt:SAMLToken><tt:KerberosToken>false</tt:KerberosToken><tt:RELToken>false</tt:RELToken></tt:Security>" +
		"</tt:Device>" +
		"<tt:Media>" +
		"<tt:XAddr>" + mediaXAddr + "</tt:XAddr>" +
		"<tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast><tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities>" +
		"<tt:Extension><tt:ProfileCapabilities><tt:MaximumNumberOfProfiles>" + fmt.Sprint(len(s.dev.Streams)) + "</tt:MaximumNumberOfProfiles></tt:ProfileCapabilities></tt:Extension>" +
		"</tt:Media>" +
		"</tds:Capabilities></tds:GetCapabilitiesResponse>"
}

// includeCapability reports whether GetServices requested embedded
// capabilities.
func includeCapability(body []byte) bool {
	v, ok := soap.ExtractElement(body, "IncludeCapability")
	return ok && strings.EqualFold(v, "true")
}

func (s *Server) services(r *http.Request, includeCap bool) string {
	entry := func(ns, path string) string {
		out := "<tds:Service>" +
			"<tds:Namespace>" + ns + "</tds:Namespace>" +
			"<tds:XAddr>" + soap.XMLEscape("http://"+s.hostPort(r)+path) + "</tds:XAddr>"
		if includeCap {
			out += "<tds:Capabilities></tds:Capabilities>"
		}
		out += "<tds:Version><tt:Major>2</tt:Major><tt:Minor>60</tt:Minor></tds:Version>" +
			"</tds:Service>"
		return out
	}
	return "<tds:GetServicesResponse>" +
		entry(soap.NSDevice, "/onvif/device_service") +
		entry(soap.NSMedia, "/onvif/media_service") +
		"</tds:GetServicesResponse>"
}

func deviceServiceCapabilities() string {
	return "<tds:GetServiceCapabilitiesResponse><tds:Capabilities>" +
		`<tds:Network IPFilter="false" ZeroConfiguration="false" IPVersion6="false" DynDNS="false" Dot11Configuration="false" HostnameFromDHCP="false" NTP="0"/>` +
		`<tds:Security TLS1.1="false" TLS1.2="false" OnboardKeyGeneration="false" AccessPolicyConfig="false" DefaultAccessPolicy="false" Dot1X="false" RemoteUserHandling="false" X.509Token="false" SAMLToken="false" KerberosToken="false" UsernameToken="true" HttpDigest="false" RELToken="false"/>` +
		`<tds:System DiscoveryResolve="false" DiscoveryBye="true" RemoteDiscovery="false" SystemBackup="false" SystemLogging="false" FirmwareUpgrade="false"/>` +
		"</tds:Capabilities></tds:GetServiceCapabilitiesResponse>"
}

func (s *Server) deviceInformation() string {
	return "<tds:GetDeviceInformationResponse>" +
		"<tds:Manufacturer>" + soap.XMLEscape(s.manufacturer()) + "</tds:Manufacturer>" +
		"<tds:Model>" + soap.XMLEscape(s.model()) + "</tds:Model>" +
		"<tds:FirmwareVersion>" + soap.XMLEscape(s.firmware()) + "</tds:FirmwareVersion>" +
		"<tds:SerialNumber>" + soap.XMLEscape(s.dev.Serial) + "</tds:SerialNumber>" +
		"<tds:HardwareId>" + soap.XMLEscape(s.dev.UUID) + "</tds:HardwareId>" +
		"</tds:GetDeviceInformationResponse>"
}

func (s *Server) scopes() string {
	items := []string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/type/Network_Video_Transmitter",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/name/" + url.PathEscape(s.dev.Name),
		"onvif://www.onvif.org/hardware/" + url.PathEscape(s.model()),
		"onvif://www.onvif.org/location/",
	}
	var b strings.Builder
	b.WriteString("<tds:GetScopesResponse>")
	for _, item := range items {
		b.WriteString("<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>")
		b.WriteString(soap.XMLEscape(item))
		b.WriteString("</tt:ScopeItem></tds:Scopes>")
	}
	b.WriteString("</tds:GetScopesResponse>")
	return b.String()
}

func (s *Server) networkInterfaces(r *http.Request) string {
	return "<tds:GetNetworkInterfacesResponse>" +
		`<tds:NetworkInterfaces token="eth0">` +
		"<tt:Enabled>true</tt:Enabled>" +
		"<tt:Info><tt:Name>eth0</tt:Name><tt:HwAddress>" + soap.XMLEscape(s.dev.MAC) + "</tt:HwAddress><tt:MTU>1500</tt:MTU></tt:Info>" +
		"<tt:IPv4><tt:Enabled>true</tt:Enabled><tt:Config>" +
		"<tt:Manual><tt:Address>" + soap.XMLEscape(s.advertiseIP(r)) + "</tt:Address><tt:PrefixLength>24</tt:PrefixLength></tt:Manual>" +
		"<tt:DHCP>false</tt:DHCP>" +
		"</tt:Config></tt:IPv4>" +
		"</tds:NetworkInterfaces>" +
		"</tds:GetNetworkInterfacesResponse>"
}

func (s *Server) networkProtocols() string {
	rtspPort := s.dev.ProxyPortFor(s.dev.PrimaryStream())
	return "<tds:GetNetworkProtocolsResponse>" +
		"<tds:NetworkProtocols><tt:Name>HTTP</tt:Name><tt:Enabled>true</tt:Enabled><tt:Port>" + fmt.Sprint(s.dev.Ports.SOAP) + "</tt:Port></tds:NetworkProtocols>" +
		"<tds:NetworkProtocols><tt:Name>RTSP</tt:Name><tt:Enabled>true</tt:Enabled><tt:Port>" + fmt.Sprint(rtspPort) + "</tt:Port></tds:NetworkProtocols>" +
		"</tds:GetNetworkProtocolsResponse>"
}

func (s *Server) hostnameInfo() string {
	return "<tds:GetHostnameResponse><tds:HostnameInformation>" +
		"<tt:FromDHCP>false</tt:FromDHCP>" +
		"<tt:Name>" + soap.XMLEscape(slug(s.dev.Name)) + "</tt:Name>" +
		"</tds:HostnameInformation></tds:GetHostnameResponse>"
}

func (s *Server) users() string {
	var b strings.Builder
	b.WriteString("<tds:GetUsersResponse>")
	if s.dev.Auth != nil {
		b.WriteString("<tds:User><tt:Username>")
		b.WriteString(soap.XMLEscape(s.dev.Auth.Username))
		b.WriteString("</tt:Username><tt:UserLevel>Administrator</tt:UserLevel></tds:User>")
	}
	b.WriteString("</tds:GetUsersResponse>")
	return b.String()
}
