import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.join(__dirname, '..', 'dist');

if (!fs.existsSync(distDir)) {
  console.error('Error: web/dist directory does not exist. Run npm run build first.');
  process.exit(1);
}

// 1. Discover all HTML files in dist
function getHtmlFiles(dir, files = []) {
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      getHtmlFiles(fullPath, files);
    } else if (entry.name.endsWith('.html')) {
      files.push(fullPath);
    }
  }
  return files;
}

const htmlFiles = getHtmlFiles(distDir);
console.log(`Discovered ${htmlFiles.length} HTML files in web/dist.`);

const inventory = [];
const internalLinks = new Set();
const externalLinks = new Set();

// Regular expressions to extract links, ids, buttons
const linkRegex = /<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)<\/a>/gis;
const idRegex = /id=["']([^"']+)["']/gi;
const buttonRegex = /<button\s+[^>]*>(.*?)<\/button>/gis;
const formRegex = /<form\s+[^>]*>(.*?)<\/form>/gis;

const pageIdMap = new Map(); // relativePath -> Set of IDs

for (const htmlFile of htmlFiles) {
  const relPath = '/' + path.relative(distDir, htmlFile).replace(/\\/g, '/');
  const pageRoute = relPath === '/index.html' ? '/' : relPath.replace(/\/index\.html$/, '');
  const content = fs.readFileSync(htmlFile, 'utf-8');

  // Collect IDs on this page
  const ids = new Set();
  let idMatch;
  while ((idMatch = idRegex.exec(content)) !== null) {
    ids.add(idMatch[1]);
  }
  pageIdMap.set(pageRoute, ids);

  // Collect Links
  let linkMatch;
  while ((linkMatch = linkRegex.exec(content)) !== null) {
    const rawHref = linkMatch[1].trim();
    const linkText = linkMatch[2].replace(/<[^>]+>/g, '').trim();

    if (rawHref.startsWith('http://') || rawHref.startsWith('https://')) {
      externalLinks.add(rawHref);
      inventory.push({ page: pageRoute, type: 'external-link', target: rawHref, text: linkText });
    } else if (rawHref.startsWith('mailto:')) {
      inventory.push({ page: pageRoute, type: 'mailto-link', target: rawHref, text: linkText });
    } else {
      internalLinks.add(rawHref);
      inventory.push({ page: pageRoute, type: 'internal-link', target: rawHref, text: linkText });
    }
  }
}

console.log(`Extracted ${inventory.length} link instances (${internalLinks.size} unique internal URLs).`);

// Start HTTP preview server to test routes
const mimeTypes = {
  '.html': 'text/html',
  '.css': 'text/css',
  '.js': 'application/javascript',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.exe': 'application/octet-stream',
  '.dmg': 'application/octet-stream'
};

const server = http.createServer((req, res) => {
  let reqPath = req.url.split('?')[0].split('#')[0];
  let filePath = path.join(distDir, reqPath);

  if (fs.existsSync(filePath) && fs.statSync(filePath).isDirectory()) {
    filePath = path.join(filePath, 'index.html');
  }

  if (!fs.existsSync(filePath) && !path.extname(filePath)) {
    filePath = filePath + '.html';
  }

  if (fs.existsSync(filePath) && fs.statSync(filePath).isFile()) {
    const ext = path.extname(filePath);
    res.writeHead(200, { 'Content-Type': mimeTypes[ext] || 'text/plain' });
    fs.createReadStream(filePath).pipe(res);
  } else {
    const errorPage = path.join(distDir, '404.html');
    if (fs.existsSync(errorPage)) {
      res.writeHead(404, { 'Content-Type': 'text/html' });
      fs.createReadStream(errorPage).pipe(res);
    } else {
      res.writeHead(404);
      res.end('404 Not Found');
    }
  }
});

server.listen(49150, async () => {
  console.log('HTTP test server running on port 49150.');
  let brokenCount = 0;

  for (const link of Array.from(internalLinks)) {
    const [pathPart, fragment] = link.split('#');
    const targetRoute = pathPart === '' ? '/' : pathPart;
    const testUrl = `http://127.0.0.1:49150${link}`;

    try {
      const res = await fetch(testUrl);
      if (res.status !== 200) {
        console.error(`[BROKEN LINK] ${link} -> HTTP ${res.status}`);
        brokenCount++;
      } else {
        // Verify anchor if present
        if (fragment) {
          const pageIds = pageIdMap.get(targetRoute) || pageIdMap.get(targetRoute === '/' ? '/' : targetRoute);
          if (!pageIds || !pageIds.has(fragment)) {
            console.error(`[BROKEN ANCHOR] ${link} -> Target ID #${fragment} not found on page ${targetRoute}`);
            brokenCount++;
          } else {
            console.log(`[PASS] ${link} (HTTP 200, ID #${fragment} verified)`);
          }
        } else {
          console.log(`[PASS] ${link} (HTTP 200)`);
        }
      }
    } catch (err) {
      console.error(`[ERROR] ${link} -> ${err.message}`);
      brokenCount++;
    }
  }

  server.close();
  console.log(`\nLink audit completed. Total broken links found: ${brokenCount}.`);
  if (brokenCount > 0) {
    process.exit(1);
  }
});
