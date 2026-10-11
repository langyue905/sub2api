import type { ReactNode } from "react";

import type { CanvasResourceKind } from "@/lib/canvas/canvas-resource-references";
import type { CanvasNodeData, CanvasNodeMetadata } from "@/types/canvas";

export type CanvasNodeResource = { kind: CanvasResourceKind; text?: string; url?: string };

export type CanvasNodeDefinition = {
    type: string;
    title: string;
    icon: ReactNode;
    description?: string;
    defaultSize: { width: number; height: number };
    defaultMetadata?: CanvasNodeMetadata;
    minimapColor?: string;
    showInCreateMenu?: boolean;
    hasSourceHandle?: boolean;
    transparentBackground?: boolean;
    interactionToggle?: boolean;
    forceInteractive?: (node: CanvasNodeData) => boolean;
    keepAspectRatio?: (node: CanvasNodeData) => boolean;
    resource?: (node: CanvasNodeData) => CanvasNodeResource | null;
};
