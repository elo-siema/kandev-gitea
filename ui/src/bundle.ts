// Frontend half of kandev-plugin-gitea. Bundled by esbuild into ui/bundle.js,
// which Kandev serves and imports as a native ES module.
import type { PluginHostApi, PluginRegistry } from "@kandev/plugin-sdk";
import { PROVIDER_ID, parseReference, toChangeRequestDetail } from "./gitea";
import { createGiteaIcon } from "./icon";
import { registerSourceControlRecipe, type SourceControlRecipeLifecycle } from "./register";

const PLUGIN_ID = "kandev-plugin-gitea";

declare global {
  interface Window {
    registerKandevPlugin(
      id: string,
      lifecycle: {
        initialize(registry: PluginRegistry, host: PluginHostApi): void;
        destroy?(): void;
      },
    ): void;
  }
}

let lifecycle: SourceControlRecipeLifecycle | undefined;

window.registerKandevPlugin(PLUGIN_ID, {
  initialize(registry, host) {
    lifecycle = registerSourceControlRecipe(registry, host, {
      providerId: PROVIDER_ID,
      label: "Gitea",
      icon: createGiteaIcon(host),
      changeRequestNoun: "pull request",
      order: 40,
      supportsDraft: true,
      parseReference,
      toChangeRequestDetail,
    });
  },
  destroy() {
    lifecycle?.destroy();
    lifecycle = undefined;
  },
});
