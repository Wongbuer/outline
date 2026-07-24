import { DOMSerializer } from "prosemirror-model";
import type { Node as ProsemirrorNode } from "prosemirror-model";
import type { NodeView, NodeViewConstructor } from "prosemirror-view";
import type { ServerStyleSheet } from "styled-components";
import type ReactNode from "@shared/editor/nodes/ReactNode";
import { toError } from "@shared/utils/error";
import Logger from "@server/logging/Logger";
import { ComponentView } from "./ComponentView";
import { extensionManager } from ".";

// Nodes where the component depends on browser behavior, such as image load
// events, and the toDOM spec is already optimized for static export.
const nodesPreferringToDOM = new Set(["image", "simple_image"]);

/**
 * Creates the ProseMirror node views map for server-side HTML export,
 * rendering nodes with their React component where available and falling back
 * to the node's `toDOM` spec when the component fails to render.
 *
 * @param sheet Stylesheet collecting the styles of rendered components.
 * @returns A map of node name to node view constructor.
 */
export function createNodeViews(
  sheet: ServerStyleSheet
): Record<string, NodeViewConstructor> {
  return Object.fromEntries(
    extensionManager.extensions
      .filter((extension: ReactNode) => extension.component)
      .filter(
        (extension: ReactNode) => !nodesPreferringToDOM.has(extension.name)
      )
      .map((extension: ReactNode) => [
        extension.name,
        (node, view, getPos, decorations) => {
          try {
            return new ComponentView(extension.component, {
              node,
              view,
              getPos,
              decorations,
              sheet,
            });
          } catch (err) {
            Logger.warn(
              `Failed to render component for node "${node.type.name}", falling back to toDOM`,
              toError(err)
            );
            return renderToDOM(node);
          }
        },
      ])
  ) as Record<string, NodeViewConstructor>;
}

function renderToDOM(node: ProsemirrorNode): NodeView {
  const spec = node.type.spec.toDOM?.(node);
  const { dom, contentDOM } = DOMSerializer.renderSpec(
    document,
    spec ?? ["span"]
  );

  if (dom instanceof HTMLElement) {
    return { dom, contentDOM, update: () => false, ignoreMutation: () => true };
  }

  const wrapper = document.createElement(node.isInline ? "span" : "div");
  wrapper.appendChild(dom);
  return {
    dom: wrapper,
    contentDOM,
    update: () => false,
    ignoreMutation: () => true,
  };
}
