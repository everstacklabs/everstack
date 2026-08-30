import { LucideIcon } from "lucide-react";
import { ComponentType, SVGProps } from "react";
import { type IconifyIcon } from "@iconify/react";

export type Icon = LucideIcon | ComponentType<SVGProps<SVGSVGElement>> | IconifyIcon;
export * as Iconify from "@iconify/react";
export * from "lucide-react";