import 'package:flutter_test/flutter_test.dart';

import 'package:gatepilot_mobile/main.dart';

void main() {
  testWidgets('默认展示中文移动审批工作台', (WidgetTester tester) async {
    await tester.pumpWidget(const GatePilotMobileApp());

    expect(find.text('审批'), findsOneWidget);
    expect(find.text('设备'), findsOneWidget);
    expect(find.text('会话'), findsOneWidget);
    expect(find.text('服务地址'), findsOneWidget);
    expect(find.text('刷新'), findsOneWidget);
  });

  testWidgets('支持切换英文', (WidgetTester tester) async {
    await tester.pumpWidget(const GatePilotMobileApp());

    await tester.tap(find.text('EN'));
    await tester.pump();

    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Devices'), findsOneWidget);
    expect(find.text('Sessions'), findsOneWidget);
    expect(find.text('Server URL'), findsOneWidget);
    expect(find.text('Refresh'), findsOneWidget);
  });
}
