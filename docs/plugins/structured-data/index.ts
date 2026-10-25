import type {LoadContext, Plugin} from '@docusaurus/types';

/**
 * Injects site-wide JSON-LD structured data (Person, WebSite, SoftwareSourceCode)
 * as a single <script type="application/ld+json"> tag on every page.
 *
 * Per-page schema (TechArticle, FAQPage) is injected separately via
 * `@docusaurus/Head` inside the relevant MDX/pages so it can carry
 * page-specific data (headline, dateModified, questions...).
 */
export default function structuredDataPlugin(
  context: LoadContext,
): Plugin<void> {
  const {siteConfig} = context;
  // the site lives under baseUrl (e.g. https://kartaladev.github.io/doger),
  // so every @id and URL must include it
  const siteUrl = `${siteConfig.url}${siteConfig.baseUrl}`.replace(/\/$/, '');

  const structuredData = {
    '@context': 'https://schema.org',
    '@graph': [
      {
        '@type': 'Person',
        '@id': `${siteUrl}/#person-samber`,
        name: 'Samuel Berthe',
        alternateName: 'samber',
        url: 'https://github.com/samber',
        sameAs: [
          'https://github.com/samber',
          'https://twitter.com/samuelberthe',
          'https://samuelberthe.substack.com',
        ],
      },
      {
        '@type': 'WebSite',
        '@id': `${siteUrl}/#website`,
        url: siteUrl,
        name: siteConfig.title,
        description:
          'Documentation for doger, a type-safe dependency injection toolkit for Go using generics.',
        inLanguage: 'en',
      },
      {
        '@type': 'SoftwareSourceCode',
        '@id': `${siteUrl}/#software`,
        name: 'doger',
        description:
          'A dependency injection toolkit for Go based on Go generics (Go 1.27+), offering a type-safe API without reflection or code generation.',
        codeRepository: 'https://github.com/kartaladev/doger',
        isBasedOn: 'https://github.com/samber/do',
        programmingLanguage: {
          '@type': 'ComputerLanguage',
          name: 'Go',
        },
        runtimePlatform: 'Go 1.27+',
        license: 'https://github.com/kartaladev/doger/blob/main/LICENSE',
        author: {'@id': `${siteUrl}/#person-samber`},
        applicationCategory: 'DeveloperApplication',
        offers: {
          '@type': 'Offer',
          price: '0',
          priceCurrency: 'USD',
        },
      },
    ],
  };

  return {
    name: 'structured-data-plugin',
    injectHtmlTags() {
      return {
        headTags: [
          {
            tagName: 'script',
            attributes: {
              type: 'application/ld+json',
            },
            innerHTML: JSON.stringify(structuredData),
          },
        ],
      };
    },
  };
}
