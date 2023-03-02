import "./App.css";
import {
    useContext,
    createContext,
    React,
    useState,
    useRef,
    useEffect,
    useCallback,
    useMemo,
} from "react";
import {
    useParams,
    Link,
    NavLink,
    useNavigate,
    useLocation,
} from "react-router-dom";
import { throttle, debounce, filter } from "underscore";
import OutsideClickHandler from "react-outside-click-handler";
import { toast } from "react-toastify";
import moment from "moment";

const toastParams = {
    className:
        "fixed bottom-0 left-0 ml-4 mb-4 bg-white rounded-md shadow-md p-4",
    bodyClassName: "flex items-center",
    progressClassName: "w-full h-1 bg-gray-300 mt-2 rounded-full",
    position: "bottom-left",
    autoClose: 5000,
    hideProgressBar: true,
    closeOnClick: true,
    pauseOnHover: true,
    draggable: false,
    progress: undefined,
};

const tailwindConfig = {
    theme: {
        screens: {
            sm: 640,
            md: 768,
            lg: 1024,
            xl: 1280,
            "2xl": 1536,
        },
    },
};



function TopBar() {
    return (
        <div className="p-2 sm:px-5 flex justify-between sticky bg-gray-100 items-center">
            <div>
                <div className="flex flex-rows items-center">
                    <Link className="align-middle hover:underline" to="/">
                        Garden
                    </Link>
                </div>
            </div>
            <div>
                {false ? (
                    <div>
                        <span className="mr-2 cursor-pointer align-middle text-gray-500 hover:underline">
                            <Link to="/admin">Admin</Link>
                        </span>
                        <span
                            className="mr-2 cursor-pointer align-middle text-gray-500 hover:underline"
                        >
                            Logout
                        </span>
                    </div>
                ) : (
                    <div
                        className="p-1 text-small cursor-pointer rounded border align-middle hover:border-gray-400"
                    >
                        Use Garden
                    </div>
                )}
            </div>
        </div>
    );
}


function SideBar() {
    return (
        <div className="hidden sm:block sm:sticky sm:top-0 sm:px-6 sm:mr-4">
            <ul className="sm:sticky sm:top-5 list-none mb-1">
                <li className="mb-2">
                    <NavLink
                        className={({ isActive }) =>
                            isActive ? "text-blue-500" : "text-gray-500"
                        }
                        to="/seedlings"
                    >
                        <span className="w-full text-sm text-inherit text-gray-500 bg-gray-100 hover:bg-gray-300 font-bold py-2 px-4 rounded-full">
                            Seedlings
                        </span>
                    </NavLink>
                </li>
            </ul>
        </div>
    );
}



function HomePage() {
    return (
        <>
            <TopBar />
            <div className="flex mt-2">
                <SideBar />
                Welcome to Garden
            </div>
        </>
    );
}

export {
    HomePage
};
