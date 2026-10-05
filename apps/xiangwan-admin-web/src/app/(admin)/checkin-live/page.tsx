// Keep a separately named static artifact so an incremental image rollout can
// replace the scanner entry without relying on the old /checkin artifact.
export { default } from "../checkin/client-page";
