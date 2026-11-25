import React from "react";
import { Link } from "react-router";

import LogoBlueIcon from "@assets/logo-blue.png";
import LogoWhiteIcon from "@assets/logo-white.svg";
import Container from "@components/Container";
import FirmwareCredit from "@components/FirmwareCredit";

interface Props {
  logoHref?: string;
  actionElement?: React.ReactNode;
}

export default function SimpleNavbar({ logoHref, actionElement }: Props) {
  return (
    <div>
      <Container>
        <div className="border-b-800/20 isolate my-4 border-b pb-4 dark:border-b-slate-300/20">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-x-2">
              <Link to={logoHref ?? "/"} className="hidden h-[26px] dark:inline-block">
                <img src={LogoWhiteIcon} alt="" className="hidden h-[26px] dark:block" />
                <img src={LogoBlueIcon} alt="" className="h-[26px] dark:hidden" />
              </Link>
              <FirmwareCredit />
            </div>
            <div>{actionElement}</div>
          </div>
        </div>
      </Container>
    </div>
  );
}
