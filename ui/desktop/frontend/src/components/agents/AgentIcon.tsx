export const AGENT_ICONS = [
  { id: 'bot', label: 'Robot' },
  { id: 'coder', label: 'Coder' },
  { id: 'support', label: 'Support' },
  { id: 'writer', label: 'Writer' },
  { id: 'translator', label: 'Translator' },
  { id: 'artist', label: 'Artist' },
  { id: 'oracle', label: 'Oracle' },
  { id: 'analyst', label: 'Analyst' },
  { id: 'teacher', label: 'Teacher' },
  { id: 'researcher', label: 'Researcher' },
  { id: 'manager', label: 'Manager' },
  { id: 'guardian', label: 'Guardian' },
  { id: 'spark', label: 'Spark' },
  { id: 'sage', label: 'Sage' },
  { id: 'cpu', label: 'CPU' },
] as const

type SizeKey = 'xs' | 'sm' | 'md' | 'lg' | 'xl'

const SVG_SIZE: Record<SizeKey, number> = {
  xs: 12,
  sm: 14,
  md: 18,
  lg: 22,
  xl: 28,
}

const FALLBACK_CLS: Record<SizeKey, string> = {
  xs: 'h-3 w-3',
  sm: 'h-3.5 w-3.5',
  md: 'h-4.5 w-4.5',
  lg: 'h-5.5 w-5.5',
  xl: 'h-7 w-7',
}

const TEXT_CLS: Record<SizeKey, string> = {
  xs: 'text-xs',
  sm: 'text-sm',
  md: 'text-base',
  lg: 'text-xl',
  xl: 'text-2xl',
}

interface AgentIconProps {
  /** Either "icon:<name>" for SVG icons, any emoji string, or empty/undefined for Bot fallback */
  emoji?: string
  size?: SizeKey
  className?: string
}

/** Inline Bot SVG (no lucide dependency in desktop) */
function BotFallback({ cls }: { cls: string }) {
  return (
    <svg className={cls} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 8V4H8" />
      <rect width="16" height="12" x="4" y="8" rx="2" />
      <path d="M2 14h2" /><path d="M20 14h2" />
      <path d="M15 13v2" /><path d="M9 13v2" />
    </svg>
  )
}

/**
 * Renders the agent icon:
 * - `icon:<name>` → SVG from /agent-icons/<name>.svg via CSS mask-image (inherits currentColor)
 * - emoji string → rendered as text span
 * - empty/undefined → Bot SVG fallback
 */
export function AgentIcon({ emoji, size = 'md', className = '' }: AgentIconProps) {
  if (emoji?.startsWith('icon:')) {
    const name = emoji.slice(5)
    const px = SVG_SIZE[size]
    return (
      <span
        className={`inline-block shrink-0 ${className}`}
        style={{
          width: px,
          height: px,
          maskImage: `url(/agent-icons/${name}.svg)`,
          WebkitMaskImage: `url(/agent-icons/${name}.svg)`,
          maskSize: 'contain',
          WebkitMaskSize: 'contain',
          maskRepeat: 'no-repeat',
          WebkitMaskRepeat: 'no-repeat',
          maskPosition: 'center',
          WebkitMaskPosition: 'center',
          backgroundColor: 'currentColor',
        } as React.CSSProperties}
        aria-label={name}
        role="img"
      />
    )
  }

  if (emoji) {
    return <span className={`leading-none ${TEXT_CLS[size]} ${className}`}>{emoji}</span>
  }

  return <BotFallback cls={`${FALLBACK_CLS[size]} ${className}`} />
}
