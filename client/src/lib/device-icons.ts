import {
  CarIcon,
  CastIcon,
  CctvIcon,
  CpuIcon,
  EthernetPortIcon,
  Gamepad2Icon,
  HardDriveIcon,
  LaptopIcon,
  LightbulbIcon,
  MonitorIcon,
  MonitorSmartphoneIcon,
  PhoneIcon,
  PlugIcon,
  PrinterIcon,
  RouterIcon,
  ServerIcon,
  SmartphoneIcon,
  SpeakerIcon,
  TabletIcon,
  ThermometerIcon,
  TvIcon,
  WashingMachineIcon,
  WatchIcon,
  WifiIcon,
  type LucideIcon,
} from "lucide-react"

import { DEVICE_ICONS, type DeviceIconName } from "@/lib/api"

export interface DeviceIconInfo {
  label: string
  Icon: LucideIcon
}

/** What each of the icons the API accepts looks like, and what it is called. */
export const DEVICE_ICON_INFO: Record<DeviceIconName, DeviceIconInfo> = {
  desktop: { label: "Desktop", Icon: MonitorIcon },
  laptop: { label: "Laptop", Icon: LaptopIcon },
  tablet: { label: "Tablet", Icon: TabletIcon },
  smartphone: { label: "Smartphone", Icon: SmartphoneIcon },
  watch: { label: "Smartwatch", Icon: WatchIcon },
  tv: { label: "TV", Icon: TvIcon },
  media_player: { label: "Media player", Icon: CastIcon },
  game_console: { label: "Gaming console", Icon: Gamepad2Icon },
  speaker: { label: "Smart speaker", Icon: SpeakerIcon },
  router: { label: "Router", Icon: RouterIcon },
  access_point: { label: "Access point", Icon: WifiIcon },
  switch: { label: "Switch", Icon: EthernetPortIcon },
  server: { label: "Server", Icon: ServerIcon },
  nas: { label: "NAS", Icon: HardDriveIcon },
  printer: { label: "Printer", Icon: PrinterIcon },
  camera: { label: "IP camera", Icon: CctvIcon },
  light: { label: "Smart light", Icon: LightbulbIcon },
  plug: { label: "Smart plug", Icon: PlugIcon },
  thermostat: { label: "Thermostat", Icon: ThermometerIcon },
  appliance: { label: "Appliance", Icon: WashingMachineIcon },
  iot: { label: "IoT device", Icon: CpuIcon },
  phone: { label: "VoIP phone", Icon: PhoneIcon },
  car: { label: "Car", Icon: CarIcon },
}

/** Shown for devices nobody picked an icon for. */
export const GENERIC_DEVICE_ICON: DeviceIconInfo = { label: "Device", Icon: MonitorSmartphoneIcon }

export function isDeviceIcon(value: string | null | undefined): value is DeviceIconName {
  return (DEVICE_ICONS as readonly (string | null | undefined)[]).includes(value)
}

/** The picked icon's picture and name, or the generic one's for none. */
export function deviceIconInfo(icon: DeviceIconName | null | undefined): DeviceIconInfo {
  return icon ? DEVICE_ICON_INFO[icon] : GENERIC_DEVICE_ICON
}
