"use client";

import { createContext, useContext, useLayoutEffect } from "react";
import { usePathname } from "next/navigation";

/** What a page that decides its own signed-out answer told the app frame. */
export interface SignedOutDeclaration {
  /** The page it was declared on. */
  pathname: string;
  /** The server marked this page open to a visitor without a session. */
  readable: boolean;
}

/** The app frame's setter for a page's declaration. Outside the frame it does nothing. */
export const SignedOutDeclarationContext = createContext<(declaration: SignedOutDeclaration) => void>(() => {});

/**
 * Tells the app frame whether the server marked this page open to a visitor
 * without a session. Only a page whose route the frame leaves to the page
 * (`isTranscriptPage`) is listened to.
 *
 * It runs as a layout effect, before the browser paints: when the page is not
 * open and nobody is signed in, the frame swaps the page for its "taking you to
 * log in" note before anything of the page is shown.
 */
export function useDeclareSignedOutReadable(readable: boolean): void {
  const declare = useContext(SignedOutDeclarationContext);
  const pathname = usePathname();
  useLayoutEffect(() => {
    declare({ pathname, readable });
  }, [declare, pathname, readable]);
}
