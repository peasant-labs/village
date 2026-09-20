import { AllLicenses, type License } from "@peasant-labs/schema";

/**
 * The privacy notice content, as data.
 *
 * The notice is the text every publish consent control points at, so it must
 * describe every license choice the product offers. The license menu is the
 * schema contract's closed set (`AllLicenses`) plus the "no license" choice
 * peasant adds. `LICENSE_TERMS` is keyed by that union, so widening the
 * contract fails type-checking here until the notice covers the new choice,
 * and the mounted-route test asserts exact membership on the rendered page.
 *
 * The wording is the operator's. Edit it only on the operator's instruction.
 */

/** Publish with all rights retained: the choice peasant offers beyond the contract menu. */
export const NO_LICENSE = "none" as const;

export type LicenseChoice = License | typeof NO_LICENSE;

/** Every choice a person can publish under, in no particular order. */
export const LICENSE_CHOICES: readonly LicenseChoice[] = [...AllLicenses, NO_LICENSE];

/** Date the notice text last changed, ISO calendar date. */
export const NOTICE_UPDATED = "2026-09-10";

export const OPERATOR = {
  name: "David Huu Pham",
  region: "British Columbia, Canada",
  contact: "david@peasantlabs.org",
} as const;

/** "Who operates Village", after the sentence that names the operator and contact. */
export const OPERATOR_PARAGRAPHS: readonly string[] = [
  "We may later establish or designate a company or nonprofit to operate Village. We will identify the new operator and notify users before transferring operation of the service. Existing public contributions will remain available under the licenses already granted. Any transfer of nonpublic personal information will follow our Privacy Notice and applicable law, including obtaining consent where required.",
];

/** Applies to every license choice; shown once. */
export const CONFIRMATION =
  "You confirm that you have the rights and permissions needed for publication and have removed credentials, confidential information, and personal information you are not authorized to disclose.";

export interface LicenseChoiceTerms {
  /** The license as the consent control names it, e.g. "CC BY 4.0". */
  name: string;
  /** The exact consent-control label the paragraph quotes. */
  controlLabel: string;
  /** The canonical license text, or null when no license is attached. */
  deedUrl: string | null;
  paragraphs: readonly string[];
}

export const LICENSE_TERMS: Readonly<Record<LicenseChoice, LicenseChoiceTerms>> = {
  "CC-BY-4.0": {
    name: "CC BY 4.0",
    controlLabel: "Publish under CC BY 4.0",
    deedUrl: "https://creativecommons.org/licenses/by/4.0/",
    paragraphs: [
      "By selecting “Publish under CC BY 4.0,” you authorize public release of the reviewed transcript and the metadata identified as public. To the extent you own or are authorized to license the applicable rights, you license that material under Creative Commons Attribution 4.0 International.",
      "Anyone, including Village’s current or future operator, may reuse the licensed material under that license. Removing a contribution from Village does not revoke permissions already granted to recipients complying with the license. Applicable privacy rights remain unaffected.",
    ],
  },
  "CC-BY-SA-4.0": {
    name: "CC BY-SA 4.0",
    controlLabel: "Publish under CC BY-SA 4.0",
    deedUrl: "https://creativecommons.org/licenses/by-sa/4.0/",
    paragraphs: [
      "By selecting “Publish under CC BY-SA 4.0,” you authorize public release of the reviewed transcript and the metadata identified as public. To the extent you own or are authorized to license the applicable rights, you license that material under Creative Commons Attribution-ShareAlike 4.0 International.",
      "Anyone, including Village’s current or future operator, may reuse the licensed material under that license and must release adapted material under the same license. Removing a contribution from Village does not revoke permissions already granted to recipients complying with the license. Applicable privacy rights remain unaffected.",
    ],
  },
  "CC0-1.0": {
    name: "CC0 1.0",
    controlLabel: "Publish under CC0 1.0",
    deedUrl: "https://creativecommons.org/publicdomain/zero/1.0/",
    paragraphs: [
      "By selecting “Publish under CC0 1.0,” you authorize public release of the reviewed transcript and the metadata identified as public. To the extent you own or are authorized to waive the applicable rights, you dedicate that material to the public domain under the Creative Commons CC0 1.0 Universal Public Domain Dedication.",
      "Anyone, including Village’s current or future operator, may reuse the material for any purpose without attribution. Removing a contribution from Village does not withdraw the dedication. Applicable privacy rights remain unaffected.",
    ],
  },
  [NO_LICENSE]: {
    name: "no license",
    controlLabel: "Publish without a license",
    deedUrl: null,
    paragraphs: [
      "By selecting “Publish without a license,” you authorize public release of the reviewed transcript and the metadata identified as public, and you keep all other rights. You grant Village and its current or future operator a non-exclusive, worldwide, royalty-free license to store, display, and make the material available to Village users for as long as it remains on Village.",
      "Anyone who wants to reuse the material beyond reading it on Village must ask you for permission. Removing a contribution ends this hosting grant for the copies Village controls. Applicable privacy rights remain unaffected.",
    ],
  },
};

/** Reading order on the page. Covers every choice; the test asserts exact membership. */
export const LICENSE_ORDER: readonly LicenseChoice[] = [
  "CC-BY-4.0",
  "CC-BY-SA-4.0",
  "CC0-1.0",
  NO_LICENSE,
];
