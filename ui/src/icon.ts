import type { PluginHostApi, PluginIcon } from "@kdlbs/plugin-sdk";

/** Teacup glyph in the host's stroke-icon style; the marketplace card uses assets/icon.svg. */
export function createGiteaIcon(host: PluginHostApi): PluginIcon {
  return function GiteaIcon({ className, "aria-hidden": ariaHidden }) {
    return host.jsx(
      "svg",
      {
        xmlns: "http://www.w3.org/2000/svg",
        width: 24,
        height: 24,
        viewBox: "0 0 24 24",
        fill: "none",
        stroke: "currentColor",
        strokeWidth: 2,
        strokeLinecap: "round",
        strokeLinejoin: "round",
        className,
        "aria-hidden": ariaHidden ?? true,
      },
      host.jsx("path", { d: "M4 10h12v5a5 5 0 0 1-5 5h-2a5 5 0 0 1-5-5z" }),
      host.jsx("path", { d: "M16 12h1.5a2.5 2.5 0 0 1 0 5H16" }),
      host.jsx("path", { d: "M10 10V6" }),
      host.jsx("path", { d: "M8 3h4v3H8z" }),
    );
  };
}
