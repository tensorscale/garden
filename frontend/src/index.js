import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import "./index.css";
import {
    HomePage,
} from "./App";
import reportWebVitals from "./reportWebVitals";
import { RouterProvider, createBrowserRouter } from "react-router-dom";
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";
import { ToastContainer } from "react-toastify";
import "react-toastify/dist/ReactToastify.css";

const router = createBrowserRouter([
    {
        path: "/",
        element: <HomePage />,
    },
]);

const queryClient = new QueryClient();

const root = ReactDOM.createRoot(document.getElementById("root"));

function App() {
    let appChild = (
        <QueryClientProvider client={queryClient}>
            <div>
                <div className="hidden sm:block col-span-1"></div>
                <div className="col-span-12 sm:col-span-10">
                    <div className="grid grid-flow-row auto-rows-max">
                        <RouterProvider router={router} />
                    </div>
                    <ToastContainer />
                </div>
                <div className="hidden sm:block sm:col-span-1"></div>
            </div>
            <div className="sm:hidden sticky border-top border bottom-0 h-12 bg-gray-100 w-full">
                <div className="grid grid-cols-4 text-center">
                    <a className="text-blue-500" href="/feed">
                        Feed
                    </a>
                    <a className="text-blue-500" href="/samples">
                        Sample
                    </a>
                    <a className="text-blue-500" href="/nodes">
                        Nodes
                    </a>
                    <a className="text-blue-500" href="/collections">
                        Collections
                    </a>
                </div>
            </div>
        </QueryClientProvider>
    );

    return (
        <div>
            {appChild}
        </div>
    );
}

root.render(
    <StrictMode>
        <App />
    </StrictMode>
);

// If you want to start measuring performance in your app, pass a function
// to log results (for example: reportWebVitals(console.log))
// or send to an analytics endpoint. Learn more: https://bit.ly/CRA-vitals
reportWebVitals();
