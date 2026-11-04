import { setupPlugin, type SetupContext } from './setup.ts';

export const PLUGIN_ID = 'second-brain.capture';

/**
 * Thin OpenCode entrypoint. `Plugin.define` is an identity helper, but importing
 * it would make the installed plugin depend on `node_modules`; a plain object
 * keeps `~/.config/opencode/plugins/second-brain/` self-contained.
 */
export default {
  id: PLUGIN_ID,
  setup: (ctx: unknown) => setupPlugin(ctx as SetupContext),
};
