import { defineCollection } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// The docs collection is the tree generated from docs/ by `rune docs-site-gen`
// (see internal/docsite). Because the generator writes into Starlight's default
// src/content/docs, the stock loader is all that is needed.
export const collections = {
  docs: defineCollection({ loader: docsLoader(), schema: docsSchema() }),
};
