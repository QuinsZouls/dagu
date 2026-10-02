import { useConfig } from '@/contexts/ConfigContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';

const defaultLicense: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};

export function useLicense(): LicenseStatus {
  const config = useConfig();
  return config?.license ?? defaultLicense;
}

export function useHasFeature(feature: string): boolean {
  const license = useLicense();
  // Server only reports non-empty features in community mode when the operator explicitly enabled them.
  return (
    license.features.includes(feature) &&
    (license.valid || license.gracePeriod || license.community)
  );
}
