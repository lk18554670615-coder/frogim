import '../core/models.dart';

/// Explicit authentication capabilities, independent of the business API.
abstract interface class TenantAuthenticationRepository {
  bool get hasPendingRegistration;
  Future<AppUser> resumeRegistration();
  Future<void> dismissPendingRegistration();
  Future<AppUser> tenantLogin(
    String phone,
    String code, {
    String enterpriseCode = '',
    String inviteCode = '',
  });
  Future<AppUser> tenantRegister({
    required String phone,
    required String code,
    required String password,
    required String name,
    String enterpriseCode = '',
    String inviteCode = '',
  });
}
