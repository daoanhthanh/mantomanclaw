interface ChannelIconProps {
  type: string;
  size?: number;
  className?: string;
}

/** Renders a channel brand icon from /channel-icons/{type}.svg. Hides itself on load error. */
export function ChannelIcon({ type, size = 20, className }: ChannelIconProps) {
  return (
    <img
      src={`/channel-icons/${type}.svg`}
      alt={type}
      width={size}
      height={size}
      className={className}
      onError={(e) => {
        (e.target as HTMLImageElement).style.display = "none";
      }}
    />
  );
}
