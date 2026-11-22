import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ShipProgress } from "./ShipProgress";
import type { ShipJob } from "../shipping-types";

it("uses confirmed stages and actual bytes, without inventing transfer percentages", () => {
  const job: ShipJob = { id:"one", planId:"plan", actor:"admin", project:"shop", status:"running", phase:"transfer", message:"", createdAt:"", updatedAt:"", steps:[{phase:"build",status:"complete"},{phase:"transfer",status:"running"},{phase:"deploy",status:"pending"}], progress:{current:1048576,unit:"bytes"} };
  const { rerender } = render(<ShipProgress job={job}/>);
  const overall = screen.getByRole("progressbar", {name:"مراحل تکمیل‌شده"});
  expect(overall).toHaveAttribute("value","1");
  expect(overall).toHaveAttribute("max","3");
  expect(screen.getByRole("progressbar",{name:"اجرای مرحلهٔ جاری"})).not.toHaveAttribute("value");
  expect(screen.getByText(/مگابایت واقعاً/)).toBeInTheDocument();
  rerender(<ShipProgress job={{...job,status:"failed",steps:[job.steps![0],{phase:"transfer",status:"failed"},job.steps![2]]}}/>);
  expect(screen.queryByRole("progressbar",{name:"اجرای مرحلهٔ جاری"})).not.toBeInTheDocument();
  expect(overall).toHaveAttribute("value","1");
});
