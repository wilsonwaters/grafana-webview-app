import fs from 'fs';
import path from 'path';
import appJson from './plugin.json';
import panelJson from './panels/webview/plugin.json';

const REPO = 'https://github.com/wilsonwaters/grafana-webview-app';

const manifests = [
  { label: 'app', json: appJson, dir: __dirname },
  { label: 'panel', json: panelJson, dir: path.join(__dirname, 'panels', 'webview') },
];

describe('plugin.json metadata (RV1-14, RV1-19)', () => {
  test('the panel skips the data query: it never reads query data', () => {
    expect(panelJson.skipDataQuery).toBe(true);
  });

  test('both manifests use the name "Web View"', () => {
    expect(appJson.name).toBe('Web View');
    expect(panelJson.name).toBe('Web View');
    expect(appJson.includes.find((i) => i.type === 'panel')?.name).toBe('Web View');
  });

  test.each(manifests)('$label: documentation, repository and issue links point to the repository', ({ json }) => {
    expect(json.info.links).toEqual([
      { name: 'Documentation', url: `${REPO}#readme` },
      { name: 'Repository', url: REPO },
      { name: 'Issues', url: `${REPO}/issues` },
    ]);
  });

  test.each(manifests)('$label: the author has a URL', ({ json }) => {
    expect(json.info.author.url).toBe('https://github.com/wilsonwaters');
  });

  test.each(manifests)('$label: has 2-3 screenshots, each a PNG that ships with the plugin', ({ json, dir }) => {
    const screenshots = json.info.screenshots;
    expect(screenshots.length).toBeGreaterThanOrEqual(2);
    expect(screenshots.length).toBeLessThanOrEqual(3);
    for (const shot of screenshots) {
      expect(shot.name.length).toBeGreaterThan(0);
      // Relative to the manifest, inside its own img folder (copied to dist by the build).
      expect(shot.path).toMatch(/^img\/screenshots\/[a-z-]+\.png$/);
      const file = path.join(dir, shot.path);
      expect(fs.existsSync(file)).toBe(true);
      expect(fs.readFileSync(file).subarray(1, 4).toString('ascii')).toBe('PNG');
    }
  });
});
