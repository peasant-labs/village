/**
 * Compile-time exhaustiveness for the Library's two closed sets.
 *
 * This file is never executed: it is typechecked, and a member added to a type
 * without being added to its `ALL_*` inventory makes one of the assignments
 * below fail to compile. A runtime count would pass a build that forgot a
 * member; this cannot.
 */
import { ALL_GROUP_BYS, ALL_VILLAGE_COLUMNS, type ColumnId, type GroupBy } from "./homeView";

/** A grouping the union declares and {@link ALL_GROUP_BYS} does not carry. */
export type MissingGroupBy = Exclude<GroupBy, (typeof ALL_GROUP_BYS)[number]>;
/** A column the union declares and {@link ALL_VILLAGE_COLUMNS} does not carry. */
export type MissingHomeColumnId = Exclude<ColumnId, (typeof ALL_VILLAGE_COLUMNS)[number]>;

const groupBysAreExhaustive: MissingGroupBy extends never ? true : never = true;
const columnsAreExhaustive: MissingHomeColumnId extends never ? true : never = true;

// A member's wire is its identity; a duplicate would make two members
// indistinguishable to storage and the URL.
const groupByWires: readonly string[] = ALL_GROUP_BYS.map((member) => member.wire);
const columnWires: readonly string[] = ALL_VILLAGE_COLUMNS.map((member) => member.wire);

void groupBysAreExhaustive;
void columnsAreExhaustive;
void groupByWires;
void columnWires;
