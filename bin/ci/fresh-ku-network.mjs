// Only the exact deliberate denial may be cancelled by the proof's re-login.
// Other requests to the same operation URL must still fail the proof.
export function isExpectedDeniedNavigationAbort(request, denial, navigating) {
  return Boolean(navigating && denial?.uiConfirmed && denial.status === 409 &&
    request === denial.request && request.method() === 'POST' &&
    request.failure()?.errorText === 'net::ERR_ABORTED');
}
