import { Stack, App, StackProps } from "@aws-cdk/core";
import {
  DatabaseCluster,
  DatabaseClusterEngine,
  ParameterGroup,
} from "@aws-cdk/aws-rds";
import {
  InstanceType,
  InstanceClass,
  InstanceSize,
  IVpc,
} from "@aws-cdk/aws-ec2";

interface Props extends StackProps {
  vpc: IVpc;
}

export class DBStack extends Stack {
  constructor(scope: App, name: string, props: Props) {
    const { vpc, ...stackProps } = props;
    super(scope, name, stackProps);

    const charsetTargets = [
      "client",
      "connection",
      "database",
      "results",
      "server",
    ];
    const charsetParams = charsetTargets.reduce<Record<string, string>>(
      (a, k) => ({
        ...a,
        [`character_set_${k}`]: "utf8mb4",
      }),
      {}
    );
    const parameterGroup = new ParameterGroup(this, "CustomParameterGroup", {
      family: "aurora-mysql5.7",
      parameters: {
        ...charsetParams,
      },
    });

    new DatabaseCluster(this, "Cluster", {
      engine: DatabaseClusterEngine.AURORA_MYSQL,
      instances: 3,
      instanceProps: {
        instanceType: InstanceType.of(InstanceClass.T3, InstanceSize.MICRO),
        parameterGroup,
        vpc,
      },
      masterUser: {
        username: "owner",
      },
      parameterGroup,
    });
  }
}
