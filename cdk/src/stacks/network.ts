import { Stack, StackProps, App } from "@aws-cdk/core";
import { Vpc, IVpc } from "@aws-cdk/aws-ec2";

type Props = StackProps;

export class NetworkStack extends Stack {
  readonly defaultVPC: IVpc;

  constructor(scope: App, name: string, props: Props) {
    super(scope, name, props);

    this.defaultVPC = new Vpc(this, "Default", {});
  }
}
