import { Plugin } from '@opencode/plugin';
import { setupPlugin, type SetupContext } from './setup.ts';

export const PLUGIN_ID = 'second-brain.capture';

/**
 * Thin OpenCode entrypoint: the SDK's branded types are adapted once here, and
 * all behaviour lives in setup.ts so it can be tested with a fake context.
 */
export default Plugin.define({
  id: PLUGIN_ID,
  setup: (ctx) => setupPlugin(ctx as unknown as SetupContext),
});
