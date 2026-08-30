// License hooks
// Will be populated with extracted hooks from apps/admin

export function useLicenseStatus() {
  return { data: null, isLoading: true, error: null }
}

export function useRefreshLicense() {
  return { mutate: () => { }, isPending: false }
}

export function useLicenseInfo() {
  return { isLocked: false, isExpiringSoon: false, isTrialExpiringSoon: false }
}

export function useTrialStatus() {
  return { isTrialMode: false, isTrialExpired: false }
}

export function useGatewayLicenseStatus() {
  return { data: null }
}



