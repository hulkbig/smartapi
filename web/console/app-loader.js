const visualVersion = 'smartapi-model-routing-20261107';
const partUrls = Array.from({ length: 15 }, (_, i) => `./app.part${i + 1}.jsfrag?visual=${visualVersion}`);
const parts = await Promise.all(partUrls.map(async url => {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Failed to load ${url}: ${response.status}`);
  return response.text();
}));

let source = parts.join('');
source = source.replace(
  "import { createAdapter } from './api.js';",
  `const { createAdapter } = await import(new URL('./api.js?visual=${visualVersion}', document.baseURI).href);`
);
source = source.replace(
  "import { scenarios } from './mock-data.js';",
  `const { scenarios } = await import(new URL('./mock-data.js?visual=${visualVersion}', document.baseURI).href);`
);

for (const [declaration,binding,file] of [
 ["import * as observabilityModel from './observability-model.js';","observabilityModel","observability-model.js"],
 ["import { ObservationResource } from './observability-state.js';","{ ObservationResource }","observability-state.js"],
 ["import { mountRouteExplanation } from './route-explanation.js';","{ mountRouteExplanation }","route-explanation.js"],
]) source=source.replace(declaration,`const ${binding} = await import(new URL('./${file}?visual=${visualVersion}',document.baseURI).href);`);

const blob = new Blob([source], { type: 'text/javascript' });
const moduleUrl = URL.createObjectURL(blob);
try {
  await import(moduleUrl);
} finally {
  URL.revokeObjectURL(moduleUrl);
}
