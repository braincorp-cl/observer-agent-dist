package shared

import (
	"os"
	"time"
)

// WinSvcResp for sending service control status back to the rmm
type WinSvcResp struct {
	Success  bool   `json:"success"`
	ErrorMsg string `json:"errormsg"`
}

type ProcessMsg struct {
	Name     string `json:"name"`
	Pid      int    `json:"pid"`
	MemBytes uint64 `json:"membytes"`
	Username string `json:"username"`
	UID      int    `json:"id"`
	CPU      string `json:"cpu_percent"`
}

type AgentConfig struct {
	BaseURL            string
	AgentID            string
	APIURL             string
	Token              string
	AgentPK            string
	PK                 int
	Cert               string
	Proxy              string
	CustomMeshDir      string
	WinTmpDir          string
	UnixTmpDir         string
	WinRunAsUserTmpDir string
	NatsProxyPath      string
	NatsProxyPort      string
	NatsStandardPort   string
	NatsPingInterval   int
	Insecure           string
}

type RunScriptResp struct {
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	Retcode  int     `json:"retcode"`
	ExecTime float64 `json:"execution_time"`
	ID       int     `json:"id"`
}

type RawCMDResp struct {
	Results string `json:"results"`
}

type AgentInfo struct {
	AgentPK      int     `json:"id"`
	Version      string  `json:"version"`
	Username     string  `json:"logged_in_username"`
	Hostname     string  `json:"hostname"`
	OS           string  `json:"operating_system"`
	Platform     string  `json:"plat"`
	TotalRAM     float64 `json:"total_ram"`
	BootTime     int64   `json:"boot_time"`
	RebootNeeded bool    `json:"needs_reboot"`
}

type PingCheckResponse struct {
	ID      int    `json:"id"`
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
	Output  string `json:"output"`
}

type WinUpdateResult struct {
	AgentID string       `json:"agent_id"`
	Updates []WUAPackage `json:"wua_updates"`
}

type WUAPackage struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Categories     []string `json:"categories"`
	CategoryIDs    []string `json:"category_ids"`
	KBArticleIDs   []string `json:"kb_article_ids"`
	MoreInfoURLs   []string `json:"more_info_urls"`
	SupportURL     string   `json:"support_url"`
	UpdateID       string   `json:"guid"`
	RevisionNumber int32    `json:"revision_number"`
	Severity       string   `json:"severity"`
	Installed      bool     `json:"installed"`
	Downloaded     bool     `json:"downloaded"`
}

type WinUpdateInstallResult struct {
	AgentID  string `json:"agent_id"`
	UpdateID string `json:"guid"`
	Success  bool   `json:"success"`
}

type SupersededUpdate struct {
	AgentID  string `json:"agent_id"`
	UpdateID string `json:"guid"`
}

type AgentNeedsReboot struct {
	AgentID     string `json:"agent_id"`
	NeedsReboot bool   `json:"needs_reboot"`
}

type ChocoInstalled struct {
	AgentID   string `json:"agent_id"`
	Installed bool   `json:"installed"`
}

// Disk holds physical disk info (fields are human-readable strings for NATS wire format)
type Disk struct {
	Device  string `json:"device"`
	Fstype  string `json:"fstype"`
	Total   string `json:"total"`
	Used    string `json:"used"`
	Free    string `json:"free"`
	Percent int    `json:"percent"`
}

type MeshNodeID struct {
	Func    string `json:"func"`
	Agentid string `json:"agent_id"`
	NodeID  string `json:"nodeid"`
}

type AssignedTask struct {
	TaskPK  int  `json:"id"`
	Enabled bool `json:"enabled"`
}

type Script struct {
	Shell     string   `json:"shell"`
	Code      string   `json:"code"`
	RunAsUser bool     `json:"run_as_user"`
	EnvVars   []string `json:"env_vars"`
}

type CheckInfo struct {
	AgentPK  int `json:"agent"`
	Interval int `json:"check_interval"`
}

type Check struct {
	Script                 Script         `json:"script"`
	AssignedTasks          []AssignedTask `json:"assigned_tasks"`
	CheckPK                int            `json:"id"`
	CheckType              string         `json:"check_type"`
	Status                 string         `json:"status"`
	Threshold              int            `json:"threshold"`
	Disk                   string         `json:"disk"`
	IP                     string         `json:"ip"`
	ScriptArgs             []string       `json:"script_args"`
	EnvVars                []string       `json:"env_vars"`
	NushellEnableConfig    bool           `json:"nushell_enable_config"`
	DenoDefaultPermissions string         `json:"deno_default_permissions"`
	Timeout                int            `json:"timeout"`
	ServiceName            string         `json:"svc_name"`
	PassStartPending       bool           `json:"pass_if_start_pending"`
	PassNotExist           bool           `json:"pass_if_svc_not_exist"`
	RestartIfStopped       bool           `json:"restart_if_stopped"`
	LogName                string         `json:"log_name"`
	EventID                int            `json:"event_id"`
	EventIDWildcard        bool           `json:"event_id_is_wildcard"`
	EventType              string         `json:"event_type"`
	EventSource            string         `json:"event_source"`
	EventMessage           string         `json:"event_message"`
	FailWhen               string         `json:"fail_when"`
	SearchLastDays         int            `json:"search_last_days"`
}

type AllChecks struct {
	CheckInfo
	Checks []Check
}

type TaskAction struct {
	ActionType             string   `json:"type"`
	Command                string   `json:"command"`
	Shell                  string   `json:"shell"`
	ScriptName             string   `json:"script_name"`
	Code                   string   `json:"code"`
	Args                   []string `json:"script_args"`
	Timeout                int      `json:"timeout"`
	RunAsUser              bool     `json:"run_as_user"`
	EnvVars                []string `json:"env_vars"`
	NushellEnableConfig    bool     `json:"nushell_enable_config"`
	DenoDefaultPermissions string   `json:"deno_default_permissions"`
}

type AutomatedTask struct {
	ID              int          `json:"id"`
	TaskActions     []TaskAction `json:"task_actions"`
	Enabled         bool         `json:"enabled"`
	ContinueOnError bool         `json:"continue_on_error"`
}

type EventLogMsg struct {
	Source    string `json:"source"`
	EventType string `json:"eventType"`
	EventID   uint32 `json:"eventID"`
	Message   string `json:"message"`
	Time      string `json:"time"`
	UID       int    `json:"uid"` // for vue
}

type SoftwareList struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	InstallDate string `json:"install_date"`
	Size        string `json:"size"`
	Source      string `json:"source"`
	Location    string `json:"location"`
	Uninstall   string `json:"uninstall"`
}

type CheckIn struct {
	Func    string `json:"func"`
	Agentid string `json:"agent_id"`
	Version string `json:"version"`
}

type CheckInSW struct {
	CheckIn
	InstalledSW []SoftwareList `json:"software"`
}

type CheckInOS struct {
	CheckIn
	Hostname     string  `json:"hostname"`
	OS           string  `json:"operating_system"`
	Platform     string  `json:"plat"`
	TotalRAM     float64 `json:"total_ram"`
	BootTime     int64   `json:"boot_time"`
	RebootNeeded bool    `json:"needs_reboot"`
}

type CheckInWinServices struct {
	CheckIn
	Services []WindowsService `json:"services"`
}

type CheckInPublicIP struct {
	CheckIn
	PublicIP string `json:"public_ip"`
}

type CheckInDisk struct {
	CheckIn
	Disks []Disk `json:"disks"`
}

type CheckInLoggedUser struct {
	CheckIn
	Username string `json:"logged_in_username"`
}

type Win32_ComputerSystemProduct struct {
	Caption           string
	Description       string
	IdentifyingNumber string
	Name              string
	SKUNumber         string
	Vendor            string
	Version           string
	UUID              string
}

type Win32_ComputerSystemEX struct {
	AdminPasswordStatus       uint16
	AutomaticManagedPagefile  bool
	AutomaticResetBootOption  bool
	AutomaticResetCapability  bool
	BootOptionOnLimit         uint16
	BootOptionOnWatchDog      uint16
	BootROMSupported          bool
	BootupState               string
	Caption                   string
	ChassisBootupState        uint16
	ChassisSKUNumber          string //
	CreationClassName         string
	CurrentTimeZone           int16
	DaylightInEffect          bool
	Description               string
	DNSHostName               string
	Domain                    string
	DomainRole                uint16
	EnableDaylightSavingsTime bool
	FrontPanelResetStatus     uint16
	HypervisorPresent         bool //
	InfraredSupported         bool
	InstallDate               time.Time
	KeyboardPasswordStatus    uint16
	Manufacturer              string
	Model                     string
	Name                      string
	NameFormat                string
	NetworkServerModeEnabled  bool
	NumberOfLogicalProcessors uint32
	NumberOfProcessors        uint32
	OEMStringArray            []string
	PartOfDomain              bool
	PauseAfterReset           int64
	PCSystemType              uint16
	PCSystemTypeEx            uint16 //
	PowerManagementSupported  bool
	PowerOnPasswordStatus     uint16
	PowerState                uint16
	PowerSupplyState          uint16
	PrimaryOwnerContact       string
	PrimaryOwnerName          string
	ResetCapability           uint16
	ResetCount                int16
	ResetLimit                int16
	Roles                     []string
	Status                    string
	SupportContactDescription []string
	SystemFamily              string //
	SystemSKUNumber           string //
	SystemType                string
	ThermalState              uint16
	TotalPhysicalMemory       uint64
	UserName                  string
	WakeUpType                uint16
	Workgroup                 string
}

type Win32_ComputerSystem struct {
	AdminPasswordStatus       uint16
	AutomaticManagedPagefile  bool
	AutomaticResetBootOption  bool
	AutomaticResetCapability  bool
	BootOptionOnLimit         uint16
	BootOptionOnWatchDog      uint16
	BootROMSupported          bool
	BootupState               string
	Caption                   string
	ChassisBootupState        uint16
	CreationClassName         string
	CurrentTimeZone           int16
	DaylightInEffect          bool
	Description               string
	DNSHostName               string
	Domain                    string
	DomainRole                uint16
	EnableDaylightSavingsTime bool
	FrontPanelResetStatus     uint16
	InfraredSupported         bool
	InstallDate               time.Time
	KeyboardPasswordStatus    uint16
	Manufacturer              string
	Model                     string
	Name                      string
	NameFormat                string
	NetworkServerModeEnabled  bool
	NumberOfLogicalProcessors uint32
	NumberOfProcessors        uint32
	OEMStringArray            []string
	PartOfDomain              bool
	PauseAfterReset           int64
	PCSystemType              uint16
	PowerManagementSupported  bool
	PowerOnPasswordStatus     uint16
	PowerState                uint16
	PowerSupplyState          uint16
	PrimaryOwnerContact       string
	PrimaryOwnerName          string
	ResetCapability           uint16
	ResetCount                int16
	ResetLimit                int16
	Roles                     []string
	Status                    string
	SupportContactDescription []string
	SystemType                string
	ThermalState              uint16
	TotalPhysicalMemory       uint64
	UserName                  string
	WakeUpType                uint16
	Workgroup                 string
}

type Win32_NetworkAdapterConfiguration struct {
	Caption                      string
	Description                  string
	SettingID                    string
	ArpAlwaysSourceRoute         bool
	ArpUseEtherSNAP              bool
	DatabasePath                 string
	DeadGWDetectEnabled          bool
	DefaultIPGateway             []string
	DefaultTOS                   uint8
	DefaultTTL                   uint8
	DHCPEnabled                  bool
	DHCPLeaseExpires             time.Time
	DHCPLeaseObtained            time.Time
	DHCPServer                   string
	DNSDomain                    string
	DNSDomainSuffixSearchOrder   []string
	DNSEnabledForWINSResolution  bool
	DNSHostName                  string
	DNSServerSearchOrder         []string
	DomainDNSRegistrationEnabled bool
	ForwardBufferMemory          uint32
	FullDNSRegistrationEnabled   bool
	IGMPLevel                    uint8
	Index                        uint32
	InterfaceIndex               uint32
	IPAddress                    []string
	IPConnectionMetric           uint32
	IPEnabled                    bool
	IPFilterSecurityEnabled      bool
	IPSecPermitIPProtocols       []string
	IPSecPermitTCPPorts          []string
	IPSecPermitUDPPorts          []string
	IPSubnet                     []string
	IPUseZeroBroadcast           bool
	KeepAliveInterval            uint32
	KeepAliveTime                uint32
	MACAddress                   string
	MTU                          uint32
	NumForwardPackets            uint32
	PMTUBHDetectEnabled          bool
	PMTUDiscoveryEnabled         bool
	ServiceName                  string
	TcpipNetbiosOptions          uint32
	TcpMaxConnectRetransmissions uint32
	TcpMaxDataRetransmissions    uint32
	TcpNumConnections            uint32
	TcpUseRFC1122UrgentPointer   bool
	TcpWindowSize                uint16
	WINSEnableLMHostsLookup      bool
	WINSHostLookupFile           string
	WINSPrimaryServer            string
	WINSScopeID                  string
	WINSSecondaryServer          string
}

type Win32_PhysicalMemoryEX struct {
	Attributes           uint32 //
	BankLabel            string
	Capacity             uint64
	Caption              string
	ConfiguredClockSpeed uint32 //
	ConfiguredVoltage    uint32 //
	CreationClassName    string
	DataWidth            uint16
	Description          string
	DeviceLocator        string
	FormFactor           uint16
	HotSwappable         bool
	InstallDate          time.Time
	InterleaveDataDepth  uint16
	InterleavePosition   uint32
	Manufacturer         string
	MaxVoltage           uint32 //
	MemoryType           uint16
	MinVoltage           uint32 //
	Model                string
	Name                 string
	OtherIdentifyingInfo string
	PartNumber           string
	PositionInRow        uint32
	PoweredOn            bool
	Removable            bool
	Replaceable          bool
	SerialNumber         string
	SKU                  string
	SMBIOSMemoryType     uint32 //
	Speed                uint32
	Status               string
	Tag                  string
	TotalWidth           uint16
	TypeDetail           uint16
	Version              string
}

type Win32_PhysicalMemory struct {
	BankLabel            string
	Capacity             uint64
	Caption              string
	CreationClassName    string
	DataWidth            uint16
	Description          string
	DeviceLocator        string
	FormFactor           uint16
	HotSwappable         bool
	InstallDate          time.Time
	InterleaveDataDepth  uint16
	InterleavePosition   uint32
	Manufacturer         string
	MemoryType           uint16
	Model                string
	Name                 string
	OtherIdentifyingInfo string
	PartNumber           string
	PositionInRow        uint32
	PoweredOn            bool
	Removable            bool
	Replaceable          bool
	SerialNumber         string
	SKU                  string
	Speed                uint32
	Status               string
	Tag                  string
	TotalWidth           uint16
	TypeDetail           uint16
	Version              string
}

type Win32_OperatingSystem struct {
	BootDevice                 string
	BuildNumber                string
	BuildType                  string
	Caption                    string
	CodeSet                    string
	CountryCode                string
	CreationClassName          string
	CSCreationClassName        string
	CSDVersion                 string
	CSName                     string
	CurrentTimeZone            int16
	Debug                      bool
	Description                string
	Distributed                bool
	EncryptionLevel            uint32
	ForegroundApplicationBoost uint8
	FreePhysicalMemory         uint64
	FreeSpaceInPagingFiles     uint64
	FreeVirtualMemory          uint64
	InstallDate                time.Time
	LastBootUpTime             time.Time
	LocalDateTime              time.Time
	Locale                     string
	Manufacturer               string
	MaxNumberOfProcesses       uint32
	MaxProcessMemorySize       uint64
	MUILanguages               []string
	Name                       string
	NumberOfLicensedUsers      uint32
	NumberOfProcesses          uint32
	NumberOfUsers              uint32
	OperatingSystemSKU         uint32
	Organization               string
	OSArchitecture             string
	OSLanguage                 uint32
	OSProductSuite             uint32
	OSType                     uint16
	OtherTypeDescription       string
	PAEEnabled                 bool
	PlusProductID              string
	PlusVersionNumber          string
	Primary                    bool
	ProductType                uint32
	RegisteredUser             string
	SerialNumber               string
	ServicePackMajorVersion    uint16
	ServicePackMinorVersion    uint16
	SizeStoredInPagingFiles    uint64
	Status                     string
	SuiteMask                  uint32
	SystemDevice               string
	SystemDirectory            string
	SystemDrive                string
	TotalSwapSpaceSize         uint64
	TotalVirtualMemorySize     uint64
	TotalVisibleMemorySize     uint64
	Version                    string
	WindowsDirectory           string
}

type Win32_BaseBoard struct {
	Caption                 string
	ConfigOptions           []string
	CreationClassName       string
	Depth                   float32
	Description             string
	Height                  float32
	HostingBoard            bool
	HotSwappable            bool
	InstallDate             time.Time
	Manufacturer            string
	Model                   string
	Name                    string
	OtherIdentifyingInfo    string
	PartNumber              string
	PoweredOn               bool
	Product                 string
	Removable               bool
	Replaceable             bool
	RequirementsDescription string
	RequiresDaughterBoard   bool
	SerialNumber            string
	SKU                     string
	SlotLayout              string
	SpecialRequirements     bool
	Status                  string
	Tag                     string
	Version                 string
	Weight                  int32
	Width                   int32
}

type Win32_BIOSEX struct {
	BIOSVersion                    []string
	BuildNumber                    string
	Caption                        string
	CodeSet                        string
	CurrentLanguage                string
	Description                    string
	EmbeddedControllerMajorVersion uint8 //
	EmbeddedControllerMinorVersion uint8 //
	IdentificationCode             string
	InstallableLanguages           uint16
	InstallDate                    time.Time
	LanguageEdition                string
	ListOfLanguages                []string
	Manufacturer                   string
	Name                           string
	OtherTargetOS                  string
	PrimaryBIOS                    bool
	ReleaseDate                    time.Time
	SerialNumber                   string
	SMBIOSBIOSVersion              string
	SMBIOSMajorVersion             uint16
	SMBIOSMinorVersion             uint16
	SMBIOSPresent                  bool
	SoftwareElementID              string
	SoftwareElementState           uint16
	Status                         string
	SystemBiosMajorVersion         uint8 //
	SystemBiosMinorVersion         uint8 //
	TargetOperatingSystem          uint16
	Version                        string
}

type Win32_BIOS struct {
	BIOSVersion           []string
	BuildNumber           string
	Caption               string
	CodeSet               string
	CurrentLanguage       string
	Description           string
	IdentificationCode    string
	InstallableLanguages  uint16
	InstallDate           time.Time
	LanguageEdition       string
	ListOfLanguages       []string
	Manufacturer          string
	Name                  string
	OtherTargetOS         string
	PrimaryBIOS           bool
	ReleaseDate           time.Time
	SerialNumber          string
	SMBIOSBIOSVersion     string
	SMBIOSMajorVersion    uint16
	SMBIOSMinorVersion    uint16
	SMBIOSPresent         bool
	SoftwareElementID     string
	SoftwareElementState  uint16
	Status                string
	TargetOperatingSystem uint16
	Version               string
}

type Win32_DiskDrive struct {
	Availability             uint16
	BytesPerSector           uint32
	CapabilityDescriptions   []string
	Caption                  string
	CompressionMethod        string
	ConfigManagerErrorCode   uint32
	ConfigManagerUserConfig  bool
	CreationClassName        string
	DefaultBlockSize         uint64
	Description              string
	DeviceID                 string
	ErrorCleared             bool
	ErrorDescription         string
	ErrorMethodology         string
	FirmwareRevision         string
	Index                    uint32
	InstallDate              time.Time
	InterfaceType            string
	LastErrorCode            uint32
	Manufacturer             string
	MaxBlockSize             uint64
	MaxMediaSize             uint64
	MediaLoaded              bool
	MediaType                string
	MinBlockSize             uint64
	Model                    string
	Name                     string
	NeedsCleaning            bool
	NumberOfMediaSupported   uint32
	Partitions               uint32
	PNPDeviceID              string
	PowerManagementSupported bool
	SCSIBus                  uint32
	SCSILogicalUnit          uint16
	SCSIPort                 uint16
	SCSITargetId             uint16
	SectorsPerTrack          uint32
	SerialNumber             string
	Signature                uint32
	Size                     uint64
	Status                   string
	StatusInfo               uint16
	SystemCreationClassName  string
	SystemName               string
	TotalCylinders           uint64
	TotalHeads               uint32
	TotalSectors             uint64
	TotalTracks              uint64
	TracksPerCylinder        uint32
}

type Win32_NetworkAdapter struct {
	AdapterType              string
	AdapterTypeID            uint16
	AutoSense                bool
	Availability             uint16
	Caption                  string
	ConfigManagerErrorCode   uint32
	ConfigManagerUserConfig  bool
	CreationClassName        string
	Description              string
	DeviceID                 string
	ErrorCleared             bool
	ErrorDescription         string
	GUID                     string
	Index                    uint32
	InstallDate              time.Time
	InterfaceIndex           uint32
	LastErrorCode            uint32
	MACAddress               string
	Manufacturer             string
	MaxNumberControlled      uint32
	MaxSpeed                 uint64
	Name                     string
	NetConnectionID          string
	NetConnectionStatus      uint16
	NetEnabled               bool
	NetworkAddresses         []string
	PermanentAddress         string
	PhysicalAdapter          bool
	PNPDeviceID              string
	PowerManagementSupported bool
	ProductName              string
	ServiceName              string
	Speed                    uint64
	Status                   string
	StatusInfo               uint16
	SystemCreationClassName  string
	SystemName               string
	TimeOfLastReset          time.Time
}

type Win32_DesktopMonitor struct {
	Availability             uint16
	Bandwidth                uint32
	Caption                  string
	ConfigManagerErrorCode   uint32
	ConfigManagerUserConfig  bool
	CreationClassName        string
	Description              string
	DeviceID                 string
	DisplayType              uint16
	ErrorCleared             bool
	ErrorDescription         string
	InstallDate              time.Time
	IsLocked                 bool
	LastErrorCode            uint32
	MonitorManufacturer      string
	MonitorType              string
	Name                     string
	PixelsPerXLogicalInch    uint32
	PixelsPerYLogicalInch    uint32
	PNPDeviceID              string
	PowerManagementSupported bool
	ScreenHeight             uint32
	ScreenWidth              uint32
	Status                   string
	StatusInfo               uint16
	SystemCreationClassName  string
	SystemName               string
}

type Win32_ProcessorEX struct {
	AddressWidth                            uint16
	Architecture                            uint16
	AssetTag                                string //
	Availability                            uint16
	Caption                                 string
	Characteristics                         uint32 //
	ConfigManagerErrorCode                  uint32
	ConfigManagerUserConfig                 bool
	CpuStatus                               uint16
	CreationClassName                       string
	CurrentClockSpeed                       uint32
	CurrentVoltage                          uint16
	DataWidth                               uint16
	Description                             string
	DeviceID                                string
	ErrorCleared                            bool
	ErrorDescription                        string
	ExtClock                                uint32
	Family                                  uint16
	InstallDate                             time.Time
	L2CacheSize                             uint32
	L2CacheSpeed                            uint32
	L3CacheSize                             uint32
	L3CacheSpeed                            uint32
	LastErrorCode                           uint32
	Level                                   uint16
	LoadPercentage                          uint16
	Manufacturer                            string
	MaxClockSpeed                           uint32
	Name                                    string
	NumberOfCores                           uint32
	NumberOfEnabledCore                     uint32 //
	NumberOfLogicalProcessors               uint32
	OtherFamilyDescription                  string
	PartNumber                              string //
	PNPDeviceID                             string
	PowerManagementSupported                bool
	ProcessorId                             string
	ProcessorType                           uint16
	Revision                                uint16
	Role                                    string
	SecondLevelAddressTranslationExtensions bool   //
	SerialNumber                            string //
	SocketDesignation                       string
	Status                                  string
	StatusInfo                              uint16
	Stepping                                string
	SystemCreationClassName                 string
	SystemName                              string
	ThreadCount                             uint32 //
	UniqueId                                string
	UpgradeMethod                           uint16
	Version                                 string
	VirtualizationFirmwareEnabled           bool //
	VMMonitorModeExtensions                 bool //
	VoltageCaps                             uint32
}

type Win32_Processor struct {
	AddressWidth              uint16
	Architecture              uint16
	Availability              uint16
	Caption                   string
	ConfigManagerErrorCode    uint32
	ConfigManagerUserConfig   bool
	CpuStatus                 uint16
	CreationClassName         string
	CurrentClockSpeed         uint32
	CurrentVoltage            uint16
	DataWidth                 uint16
	Description               string
	DeviceID                  string
	ErrorCleared              bool
	ErrorDescription          string
	ExtClock                  uint32
	Family                    uint16
	InstallDate               time.Time
	L2CacheSize               uint32
	L2CacheSpeed              uint32
	L3CacheSize               uint32
	L3CacheSpeed              uint32
	LastErrorCode             uint32
	Level                     uint16
	LoadPercentage            uint16
	Manufacturer              string
	MaxClockSpeed             uint32
	Name                      string
	NumberOfCores             uint32
	NumberOfLogicalProcessors uint32
	OtherFamilyDescription    string
	PNPDeviceID               string
	PowerManagementSupported  bool
	ProcessorId               string
	ProcessorType             uint16
	Revision                  uint16
	Role                      string
	SocketDesignation         string
	Status                    string
	StatusInfo                uint16
	Stepping                  string
	SystemCreationClassName   string
	SystemName                string
	UniqueId                  string
	UpgradeMethod             uint16
	Version                   string
	VoltageCaps               uint32
}

type Win32_USBController struct {
	Availability             uint16
	Caption                  string
	ConfigManagerErrorCode   uint32
	ConfigManagerUserConfig  bool
	CreationClassName        string
	Description              string
	DeviceID                 string
	ErrorCleared             bool
	ErrorDescription         string
	InstallDate              time.Time
	LastErrorCode            uint32
	Manufacturer             string
	MaxNumberControlled      uint32
	Name                     string
	PNPDeviceID              string
	PowerManagementSupported bool
	ProtocolSupported        uint16
	Status                   string
	StatusInfo               uint16
	SystemCreationClassName  string
	SystemName               string
	TimeOfLastReset          time.Time
}

type Win32_VideoController struct {
	AcceleratorCapabilities   []string
	AdapterCompatibility      string
	AdapterDACType            string
	AdapterRAM                uint32
	Availability              uint16
	CapabilityDescriptions    []string
	Caption                   string
	CurrentRefreshRate        uint32
	CurrentVerticalResolution uint32
	Description               string
	DeviceID                  string
	DriverDate                time.Time
	DriverVersion             string
	InstallDate               time.Time
	InstalledDisplayDrivers   string
	MaxMemorySupported        uint32
	MaxRefreshRate            uint32
	MinRefreshRate            uint32
	Name                      string
	Status                    string
	SystemCreationClassName   string
	SystemName                string
	TimeOfLastReset           time.Time
	VideoModeDescription      string
	VideoProcessor            string
}

// Win32_EncryptableVolume espeja la clase CIM del namespace
// root\CIMV2\Security\MicrosoftVolumeEncryption, que es donde vive el estado de
// BitLocker. Los ocho campos son las propiedades REALES de la clase, medidas el
// 2026-08-17 con Get-CimClass sobre tres equipos de la flota (Windows 10 Pro
// 22H2, Windows 11 Pro 23H2 y Windows 11 IoT Enterprise LTSC 24H2): identicas
// en los tres.
//
// Dos ausencias deliberadas, y ninguna es un olvido:
//
//   - El avance de la conversion (porcentaje) NO es propiedad de esta clase.
//     Sale de GetConversionStatus(), que es una invocacion COM, igual que los
//     protectores. Por eso viaja nulo en la fase 1a.
//   - Los protectores tampoco son propiedad: GetKeyProtectors() y
//     GetKeyProtectorType() son metodos. Fase 1b.
//
// El nombre del struct es el nombre de la clase a proposito: wmi.CreateQuery
// arma "SELECT <campos> FROM <nombre del tipo>", asi que renombrarlo rompe la
// consulta en silencio.
type Win32_EncryptableVolume struct {
	ConversionStatus                 uint32
	DeviceID                         string
	DriveLetter                      string
	EncryptionMethod                 uint32
	IsVolumeInitializedForProtection bool
	PersistentVolumeID               string
	ProtectionStatus                 uint32
	VolumeType                       uint32
}

// DiskEncryptionVolume es el contrato de cable por volumen. Las claves json
// tienen que ser IDENTICAS a las del espejo en natsapi/shared/types.go: un
// desalineo produce structs vacios y un UPDATE que no matchea nada, sin error.
//
// Los punteros no son decoracion. Distinguen "no se pudo leer" de un cero
// legitimo, que es justo el ok falso que esta feature tiene que evitar:
// DriveLetter nulo es un volumen sin letra (normal, y nunca es el de sistema),
// EncryptionPercentage nulo es "todavia no lo leemos" (fase 1b) y
// KeyProtectorCount nulo es "no se pudo contar", que no es lo mismo que cero
// protectores.
type DiskEncryptionVolume struct {
	DeviceID                         string   `json:"device_id"`
	DriveLetter                      *string  `json:"drive_letter"`
	ProtectionStatus                 uint32   `json:"protection_status"`
	ConversionStatus                 uint32   `json:"conversion_status"`
	EncryptionMethod                 uint32   `json:"encryption_method"`
	PersistentVolumeID               string   `json:"persistent_volume_id"`
	IsVolumeInitializedForProtection bool     `json:"is_volume_initialized_for_protection"`
	EncryptionPercentage             *uint32  `json:"encryption_percentage"`
	VolumeType                       uint32   `json:"volume_type"`
	IsSystemVolume                   bool     `json:"is_system_volume"`
	KeyProtectorCount                *int     `json:"key_protector_count"`
	KeyProtectorTypes                []uint32 `json:"key_protector_types"`
}

// DiskEncryption es el sobre que viaja dentro del mensaje agent-wmi bajo la
// clave "disk_encryption". No hay sujeto NATS nuevo ni ticker propio.
//
// Los tres campos existen para que el panel pueda separar tres cosas que se
// parecen y no lo son: un equipo que no ofrece BitLocker (Soportado=false), uno
// donde la consulta fallo (Error != nil) y uno que simplemente no tiene
// volumenes cifrables (Volumenes vacio, que es un resultado legitimo). Un
// vacio silencioso jamas debe mostrarse como "sin cifrar".
type DiskEncryption struct {
	Soportado bool                   `json:"soportado"`
	Error     *string                `json:"error"`
	Volumenes []DiskEncryptionVolume `json:"volumenes"`
}

// --- Tipos inlineados desde github.com/wh1te909/trmm-shared ---
//
// Atribucion de procedencia, NO branding: es el repo de un tercero del que se
// copiaron estos tipos cuando se elimino la dependencia externa. El nombre se
// deja tal cual porque renombrarlo fabrica una cita falsa: el rebrand de
// prefijos del producto legacy le habia aplicado el sed a este repo ajeno y lo
// dejo apuntando a `wh1te909/ormm-shared`, que no existe (404). Corregido el
// 2026-08-05. El gate de ci.yml permite esta linea explicitamente.

type CheckInNats struct {
	Agentid string `json:"agent_id"`
	Version string `json:"version"`
}

type AgentInfoNats struct {
	Agentid      string  `json:"agent_id"`
	Username     string  `json:"logged_in_username"`
	Hostname     string  `json:"hostname"`
	OS           string  `json:"operating_system"`
	Platform     string  `json:"plat"`
	TotalRAM     float64 `json:"total_ram"`
	BootTime     int64   `json:"boot_time"`
	RebootNeeded bool    `json:"needs_reboot"`
	GoArch       string  `json:"goarch"`
}

type WindowsService struct {
	Name             string `json:"name"`
	Status           string `json:"status"`
	DisplayName      string `json:"display_name"`
	BinPath          string `json:"binpath"`
	Description      string `json:"description"`
	Username         string `json:"username"`
	PID              uint32 `json:"pid"`
	StartType        string `json:"start_type"`
	DelayedAutoStart bool   `json:"autodelay"`
}

type WinSvcNats struct {
	Agentid string           `json:"agent_id"`
	WinSvcs []WindowsService `json:"services"`
}

type WinWMINats struct {
	Agentid string      `json:"agent_id"`
	WMI     interface{} `json:"wmi"`
}

type WinDisksNats struct {
	Agentid string `json:"agent_id"`
	Disks   []Disk `json:"disks"`
}

type PublicIPNats struct {
	Agentid  string `json:"agent_id"`
	PublicIP string `json:"public_ip"`
}

// GeoNats is sent by the agent on NATS subject "agent-geolocation".
// GAP-051: usa tags `json:` (NO `msgpack:`). ugorji/go/codec con MsgpackHandle
// honra `codec`/`json` e ignora `msgpack:`; las claves de wire deben ser
// idénticas a las del espejo en natsapi/shared/types.go o el decode produce un
// struct vacío (Agentid="") y el INSERT no matchea al agente. Ver CONTRACT-01.
type GeoNats struct {
	Func       string  `json:"func"`        // discriminador de check-in ("geo")
	Agentid    string  `json:"agent_id"`    // clave de match en BD
	Version    string  `json:"version"`     // versión del agente
	Lat        float64 `json:"lat"`         // latitud WGS84
	Long       float64 `json:"long"`        // longitud WGS84
	AccuracyM  int     `json:"accuracy_m"`  // precisión estimada en metros
	Source     string  `json:"source"`      // native | wifi | ip | denied | unavailable
	CapturedAt int64   `json:"captured_at"` // unix epoch (segundos, UTC)
}

type WinSoftwareList struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	InstallDate string `json:"install_date"`
	Size        string `json:"size"`
	Source      string `json:"source"`
	Location    string `json:"location"`
	Uninstall   string `json:"uninstall"`
}

type WinSoftwareNats struct {
	Agentid  string            `json:"agent_id"`
	Software []WinSoftwareList `json:"software"`
}

// FileExists returns true if the file at path exists
func FileExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// ActividadMuestra es una muestra instantánea de actividad del equipo (feature 049).
// La produce el agente de forma nativa (sin binarios del SO) corriendo en el
// contexto de la sesión del usuario, y la consume el muestreo periódico y el
// modo de diagnóstico `-m actividad-muestra`.
type ActividadMuestra struct {
	TS        string `json:"ts"`                // RFC3339 local
	SO        string `json:"so"`                // windows/macos/linux
	Sesion    string `json:"sesion_tipo"`       // x11/wayland/aqua/windows/""
	Usuario   string `json:"usuario,omitempty"` // usuario de la sesión muestreada
	Proceso   string `json:"proceso"`           // ejecutable en primer plano, minúsculas
	Titulo    string `json:"titulo,omitempty"`  // título de la ventana activa (si se pidió)
	PID       int    `json:"pid,omitempty"`
	IdleSeg   int    `json:"idle_seg"`       // segundos desde la última entrada; -1 si no medible
	Bloqueado bool   `json:"bloqueado"`      // sesión bloqueada / salvapantallas activo
	Nota      string `json:"nota,omitempty"` // p. ej. "wayland: ventana activa no disponible"
	Error     string `json:"error,omitempty"`
}

// --- Feature 049 · resumen de actividad que sube el agente -------------------
// Es el MISMO formato JSON que consumen los filtros de reporte actividad_dia /
// actividad_rango (Fase 1), para que esos reportes sirvan sin cambios.

type ActividadHora struct {
	A int            `json:"a"`           // minutos activos en la hora
	I int            `json:"i"`           // minutos inactivos
	P map[string]int `json:"p,omitempty"` // minutos por proceso en la hora
}

type ActividadUsuario struct {
	Usuario     string                   `json:"usuario"`
	Primero     string                   `json:"primero"`
	Ultimo      string                   `json:"ultimo"`
	ActivoMin   int                      `json:"activo_min"`
	InactivoMin int                      `json:"inactivo_min"`
	HuecosMin   int                      `json:"huecos_min"`
	Capturas    int                      `json:"capturas"`
	Horas       map[string]ActividadHora `json:"horas"`
	Procs       map[string]int           `json:"procs"`
	TitulosTop  [][]interface{}          `json:"titulos_top"`
	// 049-C: minutos por sitio web (deducido del título del navegador) y uso de
	// red por aplicación. Ausentes en agentes anteriores.
	Sitios map[string]int             `json:"sitios,omitempty"`
	Red    map[string]ActividadRedApp `json:"red,omitempty"`
}

// ActividadRedApp: uso de red de una aplicación en el día (049-C). Min = minutos
// con al menos una conexión establecida; ConexMax = pico de conexiones
// simultáneas; Destinos = direcciones remotas distintas.
type ActividadRedApp struct {
	Min      int `json:"min"`
	ConexMax int `json:"conex_max"`
	Destinos int `json:"destinos"`
}

type ActividadDia struct {
	Fecha    string             `json:"fecha"`
	TZ       string             `json:"tz"`
	Usuarios []ActividadUsuario `json:"usuarios"`
	Parcial  bool               `json:"parcial,omitempty"`
}

type ActividadResumen struct {
	V      int            `json:"v"`
	SO     string         `json:"so"`
	Equipo string         `json:"equipo"`
	Dias   []ActividadDia `json:"dias"`
}
