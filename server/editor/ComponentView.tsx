import type { Node as ProsemirrorNode } from "prosemirror-model";
import type { Decoration, EditorView, NodeView } from "prosemirror-view";
import type { FunctionComponent } from "react";
import ReactDOM from "react-dom";
import type { ServerStyleSheet } from "styled-components";
import { StyleSheetManager, ThemeProvider } from "styled-components";
import type { ComponentProps } from "@shared/editor/types";
import light from "@shared/styles/theme";

interface ComponentViewOptions {
  /** The node the view is responsible for. */
  node: ProsemirrorNode;
  /** The headless editor view instance. */
  view: EditorView;
  /** A function that returns the current position of the node. */
  getPos: () => number | undefined;
  /** The decorations applied to the node. */
  decorations: readonly Decoration[];
  /** Stylesheet collecting the styles of rendered components. */
  sheet: ServerStyleSheet;
}

/**
 * A ProseMirror NodeView that statically renders a node's React component for
 * server-side HTML export. The initial render is synchronous so that the
 * node's content hole is attached through `contentRef` before ProseMirror
 * renders the node's children into it.
 */
export class ComponentView implements NodeView {
  /** The DOM element that the node is rendered into. */
  dom: HTMLElement;
  /** The DOM element that the node's content is rendered into, if the node has content. */
  contentDOM: HTMLElement | null = null;

  constructor(
    component: FunctionComponent<ComponentProps>,
    options: ComponentViewOptions
  ) {
    const { node, sheet } = options;
    const Component = component;
    this.options = options;
    this.dom = document.createElement(node.type.spec.inline ? "span" : "div");
    this.dom.classList.add(`component-${node.type.name}`);

    if (!node.isLeaf) {
      this.contentDOM = document.createElement(
        node.type.spec.inline ? "span" : "div"
      );
    }

    this.applyDecorationClasses();

    try {
      ReactDOM.render(
        <StyleSheetManager sheet={sheet.instance}>
          <ThemeProvider theme={light}>
            <Component {...this.props} />
          </ThemeProvider>
        </StyleSheetManager>,
        this.dom
      );
    } catch (err) {
      ReactDOM.unmountComponentAtNode(this.dom);
      throw err;
    }
  }

  update() {
    return false;
  }

  ignoreMutation() {
    return true;
  }

  destroy() {
    ReactDOM.unmountComponentAtNode(this.dom);
    this.contentDOM = null;
  }

  private options: ComponentViewOptions;

  /**
   * Ref callback for the component to mark the element that the node's
   * content should be mounted within. The content itself is managed by
   * ProseMirror rather than React.
   */
  private handleContentRef = (element: HTMLElement | null) => {
    if (
      element &&
      this.contentDOM &&
      element !== this.contentDOM.parentElement
    ) {
      element.appendChild(this.contentDOM);
    }
  };

  /**
   * Apply classes from inline decorations, such as diff highlights, to the
   * DOM element.
   */
  private applyDecorationClasses() {
    this.options.decorations.forEach((decoration) => {
      const attrs = (
        decoration as Decoration & { type?: { attrs?: { class?: string } } }
      ).type?.attrs;
      attrs?.class?.split(" ").forEach((className) => {
        if (className) {
          this.dom.classList.add(className);
        }
      });
    });
  }

  private get props(): ComponentProps {
    return {
      theme: light,
      node: this.options.node,
      view: this.options.view,
      isSelected: false,
      isEditable: false,
      getPos: () => this.options.getPos() ?? 0,
      decorations: [...this.options.decorations],
      contentRef: this.handleContentRef,
    };
  }
}
