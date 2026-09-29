#import <Flutter/Flutter.h>

@interface GetuiflutPlugin : NSObject<FlutterPlugin>
@property FlutterMethodChannel *channel;
+ (void)handleSceneWillConnectWithOptions:(UISceneConnectionOptions *)connectionOptions;
+ (void)setVoipToken:(NSData *)token NS_SWIFT_NAME(setVoipToken(_:));
+ (NSDictionary *)voipRegistration NS_SWIFT_NAME(voipRegistration());
+ (void)handleVoipPayload:(NSDictionary *)payload NS_SWIFT_NAME(handleVoipPayload(_:));
@end
