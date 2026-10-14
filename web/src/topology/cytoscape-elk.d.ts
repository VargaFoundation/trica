// cytoscape-elk ships no types: it is one function, the extension cytoscape.use() takes.
declare module "cytoscape-elk" {
  import type cytoscape from "cytoscape";
  const elk: cytoscape.Ext;
  export default elk;
}
