/**
 * The explore route: public discovery over the whole commons.
 *
 * The list itself lives in `ExplorePage.tsx` beside this file, because two
 * routes render it: this one, and `/` for a signed-out visitor (a signed-in
 * visitor gets their own home page there instead). Keeping the body in one
 * module means the two entry points can never drift apart.
 */
export { default } from "./ExplorePage";
