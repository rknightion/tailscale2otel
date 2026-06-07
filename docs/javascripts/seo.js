/* SEO enhancements for tailscale2otel documentation */

document.addEventListener('DOMContentLoaded', function() {
  addStructuredData();
  enhanceMetaTags();
  addOpenGraphTags();
  addTwitterCardTags();
  addCanonicalURL();
});

// Add JSON-LD structured data
function addStructuredData() {
  const structuredData = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    "name": "tailscale2otel",
    "applicationCategory": "Observability / Monitoring Software",
    "operatingSystem": "Docker / Linux / macOS",
    "description": "Polls the Tailscale API and exports OpenTelemetry-native metrics + logs over OTLP, optimized for Grafana Cloud",
    "url": "https://m7kni.io/tailscale2otel/",
    "downloadUrl": "https://github.com/rknightion/tailscale2otel",
    "softwareVersion": "latest",
    "programmingLanguage": [
      "Go"
    ],
    "license": "https://github.com/rknightion/tailscale2otel/blob/main/LICENSE",
    "author": {
      "@type": "Person",
      "name": "Rob Knighton",
      "url": "https://github.com/rknightion"
    },
    "maintainer": {
      "@type": "Person",
      "name": "Rob Knighton",
      "url": "https://github.com/rknightion"
    },
    "codeRepository": "https://github.com/rknightion/tailscale2otel",
    "runtimePlatform": [
      "Go",
      "Docker"
    ],
    "applicationSubCategory": [
      "OpenTelemetry Exporter",
      "Tailscale Observability",
      "Metrics Collection"
    ],
    "offers": {
      "@type": "Offer",
      "price": "0",
      "priceCurrency": "USD"
    },
    "screenshot": "https://m7kni.io/tailscale2otel/assets/social-card.png",
    "featureList": [
      "Tailscale API polling",
      "OpenTelemetry metrics + logs over OTLP",
      "Grafana Cloud optimized",
      "Flow log and audit log streaming",
      "Node metrics scraping",
      "Docker single-binary deployment",
      "Prometheus-compatible metrics"
    ]
  };

  const docData = {
    "@context": "https://schema.org",
    "@type": "TechArticle",
    "headline": document.title,
    "description": document.querySelector('meta[name="description"]')?.content || "tailscale2otel documentation",
    "url": window.location.href,
    "datePublished": document.querySelector('meta[name="date"]')?.content,
    "dateModified": document.querySelector('meta[name="git-revision-date-localized"]')?.content,
    "author": {
      "@type": "Person",
      "name": "Rob Knighton"
    },
    "publisher": {
      "@type": "Organization",
      "name": "tailscale2otel",
      "url": "https://m7kni.io/tailscale2otel/"
    },
    "mainEntityOfPage": {
      "@type": "WebPage",
      "@id": window.location.href
    },
    "articleSection": getDocumentationSection(),
    "keywords": getPageKeywords(),
    "about": {
      "@type": "SoftwareApplication",
      "name": "tailscale2otel"
    }
  };

  const script1 = document.createElement('script');
  script1.type = 'application/ld+json';
  script1.textContent = JSON.stringify(structuredData);
  document.head.appendChild(script1);

  const script2 = document.createElement('script');
  script2.type = 'application/ld+json';
  script2.textContent = JSON.stringify(docData);
  document.head.appendChild(script2);
}

// Enhance existing meta tags
function enhanceMetaTags() {
  if (!document.querySelector('meta[name="robots"]')) {
    addMetaTag('name', 'robots', 'index, follow, max-snippet:-1, max-image-preview:large, max-video-preview:-1');
  }

  addMetaTag('name', 'language', 'en');
  addMetaTag('http-equiv', 'Content-Type', 'text/html; charset=utf-8');

  if (!document.querySelector('meta[name="viewport"]')) {
    addMetaTag('name', 'viewport', 'width=device-width, initial-scale=1');
  }

  const keywords = getPageKeywords();
  if (keywords) {
    addMetaTag('name', 'keywords', keywords);
  }

  if (isDocumentationPage()) {
    addMetaTag('name', 'article:tag', 'tailscale');
    addMetaTag('name', 'article:tag', 'opentelemetry');
    addMetaTag('name', 'article:tag', 'otlp');
    addMetaTag('name', 'article:tag', 'grafana-cloud');
  }
}

// Add Open Graph tags
function addOpenGraphTags() {
  const title = document.title || 'tailscale2otel';
  const description = document.querySelector('meta[name="description"]')?.content ||
    'Polls the Tailscale API and exports OpenTelemetry-native metrics + logs over OTLP, optimized for Grafana Cloud';
  const url = window.location.href;
  const siteName = 'tailscale2otel Documentation';

  addMetaTag('property', 'og:type', 'website');
  addMetaTag('property', 'og:site_name', siteName);
  addMetaTag('property', 'og:title', title);
  addMetaTag('property', 'og:description', description);
  addMetaTag('property', 'og:url', url);
  addMetaTag('property', 'og:locale', 'en_US');
  addMetaTag('property', 'og:image', 'https://m7kni.io/tailscale2otel/assets/social-card.png');
  addMetaTag('property', 'og:image:width', '1200');
  addMetaTag('property', 'og:image:height', '630');
  addMetaTag('property', 'og:image:alt', 'tailscale2otel - OpenTelemetry exporter for Tailscale');
}

// Add Twitter Card tags
function addTwitterCardTags() {
  const title = document.title || 'tailscale2otel';
  const description = document.querySelector('meta[name="description"]')?.content ||
    'Polls the Tailscale API and exports OpenTelemetry-native metrics + logs over OTLP, optimized for Grafana Cloud';

  addMetaTag('name', 'twitter:card', 'summary_large_image');
  addMetaTag('name', 'twitter:title', title);
  addMetaTag('name', 'twitter:description', description);
  addMetaTag('name', 'twitter:image', 'https://m7kni.io/tailscale2otel/assets/social-card.png');
  addMetaTag('name', 'twitter:creator', '@rknightion');
  addMetaTag('name', 'twitter:site', '@rknightion');
}

// Add canonical URL
function addCanonicalURL() {
  if (!document.querySelector('link[rel="canonical"]')) {
    const canonical = document.createElement('link');
    canonical.rel = 'canonical';
    canonical.href = window.location.href;
    document.head.appendChild(canonical);
  }
}

// Helper functions
function addMetaTag(attribute, name, content) {
  if (!document.querySelector(`meta[${attribute}="${name}"]`)) {
    const meta = document.createElement('meta');
    meta.setAttribute(attribute, name);
    meta.content = content;
    document.head.appendChild(meta);
  }
}

function getDocumentationSection() {
  const path = window.location.pathname;
  if (path.includes('/metrics/')) return 'Metrics Reference';
  if (path.includes('/env-vars/')) return 'Environment Variables';
  if (path.includes('/architecture/')) return 'Architecture';
  if (path.includes('/configuration/')) return 'Configuration';
  if (path.includes('/installation/')) return 'Installation';
  if (path.includes('/getting-started/')) return 'Getting Started';
  if (path.includes('/node-metrics/')) return 'Node Metrics';
  if (path.includes('/streaming-webhooks/')) return 'Streaming & Webhooks';
  if (path.includes('/dashboards/')) return 'Dashboards';
  if (path.includes('/alerts/')) return 'Alerts';
  if (path.includes('/troubleshooting/')) return 'Troubleshooting';
  if (path.includes('/security/')) return 'Security';
  return 'Documentation';
}

function getPageKeywords() {
  const path = window.location.pathname;

  let keywords = ['tailscale', 'opentelemetry', 'otlp', 'grafana cloud', 'observability', 'metrics'];

  if (path.includes('/metrics/')) keywords.push('prometheus', 'promql', 'metric-catalog');
  if (path.includes('/env-vars/')) keywords.push('environment-variables', 'configuration', 'docker');
  if (path.includes('/architecture/')) keywords.push('go', 'binary', 'collector', 'pipeline');
  if (path.includes('/configuration/')) keywords.push('yaml', 'config', 'oauth', 'api-key');
  if (path.includes('/installation/')) keywords.push('installation', 'docker-compose', 'setup', 'helm');
  if (path.includes('/getting-started/')) keywords.push('tutorial', 'quick-start', 'guide');
  if (path.includes('/node-metrics/')) keywords.push('node-metrics', 'scrape', 'derp');
  if (path.includes('/streaming-webhooks/')) keywords.push('streaming', 'webhooks', 'flow-logs', 'audit-logs');
  if (path.includes('/troubleshooting/')) keywords.push('troubleshooting', 'debugging', 'errors');

  return keywords.join(', ');
}

function isDocumentationPage() {
  return !window.location.pathname.endsWith('/') ||
         window.location.pathname.includes('/docs/');
}
