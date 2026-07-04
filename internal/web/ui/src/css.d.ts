// esbuild resolves `import './app.css'` and extracts it into the bundle's
// sibling stylesheet; this declaration lets tsc treat the import as side-effect
// only.
declare module "*.css";
