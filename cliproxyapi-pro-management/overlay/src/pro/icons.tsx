import type { SVGProps } from 'react';

export interface ProIconProps extends SVGProps<SVGSVGElement> {
  size?: number;
}

const baseSvgProps: SVGProps<SVGSVGElement> = {
  xmlns: 'http://www.w3.org/2000/svg',
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': 'true',
  focusable: 'false',
};

export function IconChartColumnIncreasing({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <path d="M3 3v18h18" />
      <path d="M7 16v1" />
      <path d="M11 12v5" />
      <path d="M15 8v9" />
      <path d="M19 4v13" />
    </svg>
  );
}

export function IconSidebarMonitor({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <path d="M3 12h3l2.2-4.5 4.2 9 2.4-5h6.2" />
      <path d="M4 19h16" />
      <path d="M4 5h16" fill="currentColor" fillOpacity="0.08" />
    </svg>
  );
}

export function IconSidebarAccountInspection({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <rect x="5" y="3" width="11" height="16" rx="2" />
      <path d="M9 7h3" />
      <path d="m8.5 11 1.4 1.4 2.6-2.8" />
      <circle cx="16.5" cy="16.5" r="3" />
      <path d="m19 19 2 2" />
      <path d="M8 3.5h5" fill="currentColor" fillOpacity="0.08" />
    </svg>
  );
}

export function IconSidebarRouting({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <path d="M6 3v18" />
      <path d="M6 5h9l3 3-3 3H6" />
      <path d="M6 13h6l3 3-3 3H6" />
    </svg>
  );
}

export function IconSidebarAccountPolicy({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <rect x="3" y="4" width="18" height="16" rx="2.5" />
      <circle cx="8.5" cy="9.5" r="2.5" />
      <path d="M5.5 16c.7-2 1.7-3 3-3s2.3 1 3 3" />
      <path d="M15 8h3" />
      <path d="M15 12h3" />
      <path d="M15 16h3" />
    </svg>
  );
}

export function IconSidebarAPIKeyPolicy({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <circle cx="9" cy="13" r="4" />
      <path d="m12 10 7-7" />
      <path d="m16 6 2 2" />
      <path d="m14 8 2 2" />
      <path d="M6 16 3 19v2h2l3-3" />
    </svg>
  );
}

export function IconSidebarProxyPool({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <rect x="3" y="4" width="18" height="6" rx="2" />
      <rect x="3" y="14" width="18" height="6" rx="2" />
      <path d="M7 7h.01" />
      <path d="M7 17h.01" />
      <path d="M11 7h6" />
      <path d="M11 17h6" />
    </svg>
  );
}

export function IconSidebarDataManagement({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <ellipse cx="12" cy="5" rx="8" ry="3" />
      <path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5" />
      <path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" />
      <path d="M8 9.5h.01" />
      <path d="M8 15.5h.01" />
    </svg>
  );
}

export function IconCopy({ size = 20, ...props }: ProIconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
    </svg>
  );
}
