/*
 * Root webpack config that EXTENDS the scaffolded base config in
 * `.config/webpack/webpack.config.ts` (which must not be edited).
 *
 * See: https://grafana.com/developers/plugin-tools/how-to-guides/extend-configurations#extend-the-webpack-config
 *
 * It adds a CopyWebpackPlugin pattern so the nested "webview" panel ships its
 * own logo at `dist/panels/webview/img/logo.svg`. The pinned
 * `.config/bundler/copyFiles.ts` only copies the app (parent) `src/img`, so the
 * nested panel's assets would otherwise be missing from `dist`, forcing the
 * dotted `../../img/logo.svg` path that `@grafana/plugin-validator` rejects
 * (metadatapaths / invalid-relative-path).
 */
import CopyWebpackPlugin from 'copy-webpack-plugin';
import path from 'path';
import type { Configuration } from 'webpack';

import grafanaConfig, { type Env } from './.config/webpack/webpack.config';
import { SOURCE_DIR } from './.config/bundler/constants';

const config = async (env: Env): Promise<Configuration> => {
  const baseConfig = await grafanaConfig(env);

  baseConfig.plugins = [
    ...(baseConfig.plugins ?? []),
    new CopyWebpackPlugin({
      patterns: [
        {
          // Copy the nested panel's own assets (`src/panels/webview/img/**`)
          // into `dist/panels/webview/img/`, preserving relative structure.
          from: '**/*',
          to: path.join('panels', 'webview', 'img'),
          context: path.resolve(process.cwd(), SOURCE_DIR, 'panels', 'webview', 'img'),
        },
      ],
    }),
  ];

  return baseConfig;
};

export default config;
